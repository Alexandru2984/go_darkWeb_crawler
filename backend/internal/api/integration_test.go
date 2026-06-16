package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

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

// newAPI spins up the full router against a fresh, migrated, truncated Postgres
// at $TEST_DATABASE_URL. Skips when the env var is unset.
func newAPI(t *testing.T) (http.Handler, *database.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping API authorization integration test")
	}
	db, err := database.InitDB(dsn)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	if _, err := db.Conn.Exec(`TRUNCATE nodes, edges, auth_audit, blacklist, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() { db.Conn.Close() })

	eng := crawler.NewEngine(db, "127.0.0.1:9050", 1, 1) // not Start()ed
	h := New(Config{
		DB:          db,
		Engine:      eng,
		CORSOrigins: []string{"http://localhost"},
	})
	return h, db
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
	tok, err := auth.GenerateToken(u.ID, u.Email, u.Role, u.TokenVersion)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	return u.ID, tok
}

func do(t *testing.T, h http.Handler, method, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

const (
	urlA = "http://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaad.onion/"
	urlB = "http://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbd.onion/"
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
	if rr := do(t, h, "GET", "/api/node?url="+url.QueryEscape(urlA), aTok); rr.Code != http.StatusOK {
		t.Errorf("A reading own node: got %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	// A reads B's node → 404 (not visible across tenants).
	if rr := do(t, h, "GET", "/api/node?url="+url.QueryEscape(urlB), aTok); rr.Code != http.StatusNotFound {
		t.Errorf("A reading B's node: got %d, want 404 (body=%s)", rr.Code, rr.Body.String())
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
