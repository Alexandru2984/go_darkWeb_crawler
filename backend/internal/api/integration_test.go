package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"onion-spider/internal/auth"
	"onion-spider/internal/crawler"
	"onion-spider/internal/database"
)

// TestMain initializes the JWT secret once for the whole api test binary so
// auth.GenerateToken works in the integration tests below. (auth.MustInitSecrets
// os.Exit(1)s if JWT_SECRET is missing, so it must be set first.)
func TestMain(m *testing.M) {
	os.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	auth.MustInitSecrets()
	os.Exit(m.Run())
}

// testDBAdvisoryLock is an arbitrary constant shared with internal/database.
// Both packages take this same Postgres advisory lock so their test runs cannot
// overlap on a shared TEST_DATABASE_URL. Keep the two values identical.
const testDBAdvisoryLock = 0x0170_9105

// lockTestDB serialises this test against the other package's test binary.
//
// `go test ./...` runs packages as concurrent processes, and internal/api and
// internal/database both TRUNCATE the same TEST_DATABASE_URL — so each was
// wiping the other's fixtures mid-test, failing whichever lost the race, a
// different one each run. CI was passing on luck.
//
// The lock is taken on a dedicated connection pulled out of the pool, not on
// the pool itself. Advisory locks are session-scoped, and a pooled connection
// can be handed to unrelated queries or recycled, which would drop the lock
// early. It must also be released explicitly: returning the connection to the
// pool leaves the session — and therefore the lock — alive.
//
// Do NOT implement this by pinning SetMaxOpenConns(1) instead. Any code path
// that needs a second connection while holding the first (streaming rows while
// issuing another query, as the export handlers do) then deadlocks against
// itself, which is exactly what the first attempt at this did.
func lockTestDB(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("checkout lock connection: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, testDBAdvisoryLock); err != nil {
		conn.Close()
		t.Fatalf("acquire test-db advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, testDBAdvisoryLock)
		conn.Close()
	})
}

// newAPI spins up the full router against a fresh, migrated, truncated Postgres
// at $TEST_DATABASE_URL. Skips when the env var is unset.
func newAPI(t *testing.T, overrides ...func(*Config)) (http.Handler, *database.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping API authorization integration test")
	}
	// The lock is taken BEFORE the schema is built, not after.
	//
	// InitDB applies the migrations, and the migration test in internal/database
	// runs them back down — dropping every table. Migrating first and locking
	// second leaves a window between the two where that teardown lands, and this
	// fixture then truncates a schema that no longer exists. It failed as
	// "relation \"nodes\" does not exist" in roughly one full-suite run in two.
	// Everything that touches the schema has to happen inside the lock.
	lockConn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open (lock): %v", err)
	}
	t.Cleanup(func() { lockConn.Close() })
	lockTestDB(t, lockConn)

	db, err := database.InitDB(dsn)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	if _, err := db.Conn.Exec(`TRUNCATE nodes, edges, auth_audit, blacklist, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() { db.Conn.Close() })

	eng := crawler.NewEngine(db, "127.0.0.1:9050", 1, 1) // not Start()ed
	cfg := Config{
		DB:          db,
		Engine:      eng,
		CORSOrigins: []string{"http://localhost"},
	}
	for _, apply := range overrides {
		apply(&cfg)
	}
	return New(cfg), db
}

// newAPIWithConfig is newAPI with the handler configuration adjusted, for tests
// that exercise a policy switch rather than the default posture.
func newAPIWithConfig(t *testing.T, apply func(*Config)) (http.Handler, *database.DB) {
	t.Helper()
	return newAPI(t, apply)
}

// mkUser creates a user with the given role and returns (id, bearer token).
func mkUser(t *testing.T, db *database.DB, email, role string) (int, string) {
	t.Helper()
	if err := db.CreateUser(email, "hash-placeholder", role, ""); err != nil {
		t.Fatalf("CreateUser(%s): %v", email, err)
	}
	u, err := db.GetUserByEmail(email)
	if err != nil || u == nil {
		t.Fatalf("GetUserByEmail(%s): %v", email, err)
	}
	tok, err := auth.GenerateToken(u.ID, u.TokenVersion, "")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	return u.ID, tok
}

func do(t *testing.T, h http.Handler, method, target, token string) *httptest.ResponseRecorder {
	return doWithBody(t, h, method, target, token, nil)
}

func doWithBody(t *testing.T, h http.Handler, method, target, token string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

const (
	urlA = "http://pg6mmjiyjmcrsslvykfwnntlaru7p5svn6y2ymmju6nubxndf4pscryd.onion/"
	urlB = "http://sp3k262uwy4r2k3ycr5awluarykdpag6a7y33jxop4cs2lu5uz5sseqd.onion/"
)

// TestAuthz_NodeIsolation: user A must not be able to read user B's node, even
// by guessing the exact URL.
func TestAuthz_NodeIsolation(t *testing.T) {
	h, db := newAPI(t)
	aID, aTok := mkUser(t, db, "a@example.com", "user")
	bID, _ := mkUser(t, db, "b@example.com", "user")

	if _, err := db.SaveNode(urlA, "A title", "", 200, "completed", "{}", "secret A", "wiki", aID); err != nil {
		t.Fatalf("SaveNode A: %v", err)
	}
	if _, err := db.SaveNode(urlB, "B title", "", 200, "completed", "{}", "secret B", "wiki", bID); err != nil {
		t.Fatalf("SaveNode B: %v", err)
	}

	// A reads its own node → 200.
	if rr := doWithBody(t, h, "POST", "/api/node", aTok, strings.NewReader(`{"url":"`+urlA+`"}`)); rr.Code != http.StatusOK {
		t.Errorf("A reading own node: got %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	// A reads B's node → 404 (not visible across tenants).
	if rr := doWithBody(t, h, "POST", "/api/node", aTok, strings.NewReader(`{"url":"`+urlB+`"}`)); rr.Code != http.StatusNotFound {
		t.Errorf("A reading B's node: got %d, want 404 (body=%s)", rr.Code, rr.Body.String())
	}
	// The POST search transport retains the same tenant boundary: a query that
	// matches both fixtures must return only A's result.
	rr := doWithBody(t, h, "POST", "/api/search", aTok, strings.NewReader(`{"q":"secret"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("A searching own nodes: got %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, urlA) || strings.Contains(body, urlB) {
		t.Errorf("search crossed tenant boundary; body=%s", body)
	}
	// Sensitive lookup values are body-only: the old query-string surface is
	// intentionally absent so browser/proxy URL logs cannot retain them.
	if rr := do(t, h, "GET", "/api/node?url=must-not-appear-in-a-request-url", aTok); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("legacy GET node lookup: got %d, want 405", rr.Code)
	}
	if rr := do(t, h, "GET", "/api/search?q=must-not-appear-in-a-request-url", aTok); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("legacy GET search: got %d, want 405", rr.Code)
	}
}

// TestAuthz_NodesListIsolation: the list endpoint returns only the caller's
// own nodes.
func TestAuthz_NodesListIsolation(t *testing.T) {
	h, db := newAPI(t)
	aID, aTok := mkUser(t, db, "a@example.com", "user")
	bID, _ := mkUser(t, db, "b@example.com", "user")
	if _, err := db.SaveNode(urlA, "A", "", 200, "completed", "{}", "a", "wiki", aID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveNode(urlB, "B", "", 200, "completed", "{}", "b", "wiki", bID); err != nil {
		t.Fatal(err)
	}
	rr := do(t, h, "GET", "/api/nodes", aTok)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, urlA) {
		t.Errorf("A's list should contain its own node; body=%s", body)
	}
	if strings.Contains(body, urlB) {
		t.Errorf("A's list leaked B's node; body=%s", body)
	}
}

// TestAuthz_BlacklistRequiresAdmin: blacklist endpoints are admin-only.
func TestAuthz_BlacklistRequiresAdmin(t *testing.T) {
	h, db := newAPI(t)
	_, userTok := mkUser(t, db, "user@example.com", "user")
	_, adminTok := mkUser(t, db, "admin@example.com", "admin")

	if rr := do(t, h, "GET", "/api/blacklist", userTok); rr.Code != http.StatusForbidden {
		t.Errorf("non-admin blacklist: got %d, want 403", rr.Code)
	}
	if rr := do(t, h, "GET", "/api/blacklist", adminTok); rr.Code != http.StatusOK {
		t.Errorf("admin blacklist: got %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestAuthz_Unauthenticated: protected endpoints reject requests with no token.
func TestAuthz_Unauthenticated(t *testing.T) {
	h, _ := newAPI(t)
	if rr := do(t, h, "GET", "/api/nodes", ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: got %d, want 401", rr.Code)
	}
}

func TestLoginCredentialModes(t *testing.T) {
	h, db := newAPI(t)
	const (
		emailAddress = "login@example.com"
		password     = "Correct-Horse-9!"
	)
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := db.CreateUser(emailAddress, hash, "user", "verification-token"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := db.Conn.Exec(`UPDATE users SET is_verified=TRUE WHERE email=$1`, emailAddress); err != nil {
		t.Fatalf("verify fixture: %v", err)
	}

	login := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-Proto", "https")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	cookieResponse := login(`{"email":"` + emailAddress + `","password":"` + password + `"}`)
	if cookieResponse.Code != http.StatusOK {
		t.Fatalf("default cookie login: got %d, body=%s", cookieResponse.Code, cookieResponse.Body.String())
	}
	var cookieBody map[string]string
	if err := json.Unmarshal(cookieResponse.Body.Bytes(), &cookieBody); err != nil {
		t.Fatalf("decode cookie login: %v", err)
	}
	if _, exposed := cookieBody["token"]; exposed {
		t.Fatal("default browser login exposed its bearer credential in the JSON body")
	}
	if len(cookieResponse.Result().Cookies()) != 2 {
		t.Fatalf("default browser login set %d cookies, want session and CSRF", len(cookieResponse.Result().Cookies()))
	}

	bearerResponse := login(`{"email":"` + emailAddress + `","password":"` + password + `","mode":"bearer"}`)
	if bearerResponse.Code != http.StatusOK {
		t.Fatalf("bearer login: got %d, body=%s", bearerResponse.Code, bearerResponse.Body.String())
	}
	var bearerBody map[string]string
	if err := json.Unmarshal(bearerResponse.Body.Bytes(), &bearerBody); err != nil {
		t.Fatalf("decode bearer login: %v", err)
	}
	if bearerBody["token"] == "" {
		t.Fatal("explicit bearer mode did not return a token")
	}
	if len(bearerResponse.Result().Cookies()) != 0 {
		t.Fatal("explicit bearer mode also set ambient session cookies")
	}
	if _, err := auth.ValidateToken(bearerBody["token"]); err != nil {
		t.Fatalf("bearer mode returned an invalid token: %v", err)
	}

	invalidMode := login(`{"email":"` + emailAddress + `","password":"` + password + `","mode":"both"}`)
	if invalidMode.Code != http.StatusBadRequest {
		t.Fatalf("invalid login mode: got %d, want 400", invalidMode.Code)
	}
}

// TestAuthz_RevokedTokenRejected: after token_version is bumped (logout-all /
// password reset), a previously-valid token is rejected with 401.
func TestAuthz_RevokedTokenRejected(t *testing.T) {
	h, db := newAPI(t)
	id, tok := mkUser(t, db, "u@example.com", "user")

	if rr := do(t, h, "GET", "/api/nodes", tok); rr.Code != http.StatusOK {
		t.Fatalf("fresh token: got %d, want 200", rr.Code)
	}
	if err := db.BumpTokenVersion(id); err != nil {
		t.Fatalf("BumpTokenVersion: %v", err)
	}
	if rr := do(t, h, "GET", "/api/nodes", tok); rr.Code != http.StatusUnauthorized {
		t.Errorf("revoked token: got %d, want 401", rr.Code)
	}
}

// TestLoginUpgradesLegacyBcryptHash proves the migration path: a user whose
// password predates Argon2id logs in normally, has their stored hash rewritten
// in place, and keeps the session they just obtained.
func TestLoginUpgradesLegacyBcryptHash(t *testing.T) {
	h, db := newAPI(t)
	const (
		emailAddress = "legacy@example.com"
		password     = "Correct-Horse-9!"
	)
	legacy, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		t.Fatalf("bcrypt fixture: %v", err)
	}
	if err := db.CreateUser(emailAddress, string(legacy), "user", "verification-token"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := db.Conn.Exec(`UPDATE users SET is_verified=TRUE WHERE email=$1`, emailAddress); err != nil {
		t.Fatalf("verify fixture: %v", err)
	}
	before, err := db.GetUserByEmail(emailAddress)
	if err != nil || before == nil {
		t.Fatalf("read fixture: %v", err)
	}
	tokenVersionBefore := before.TokenVersion

	login := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"email":"`+emailAddress+`","password":"`+password+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-Proto", "https")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	if rr := login(); rr.Code != http.StatusOK {
		t.Fatalf("legacy login: got %d, body=%s", rr.Code, rr.Body.String())
	}

	after, err := db.GetUserByEmail(emailAddress)
	if err != nil || after == nil {
		t.Fatalf("re-read user: %v", err)
	}
	if !strings.HasPrefix(after.PasswordHash, "$argon2id$") {
		t.Fatalf("hash was not upgraded, still %.10q", after.PasswordHash)
	}
	if auth.NeedsRehash(after.PasswordHash) {
		t.Fatal("upgraded hash still reports as needing a rehash")
	}
	// Re-hashing is not a credential change: other sessions must survive it.
	if after.TokenVersion != tokenVersionBefore {
		t.Fatalf("token_version moved from %d to %d — the upgrade signed other sessions out",
			tokenVersionBefore, after.TokenVersion)
	}
	// The password itself is unchanged, so it must still work against the new hash.
	if rr := login(); rr.Code != http.StatusOK {
		t.Fatalf("login after upgrade: got %d, body=%s", rr.Code, rr.Body.String())
	}
}
