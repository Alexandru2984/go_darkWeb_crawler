package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// testDBAdvisoryLock is an arbitrary constant shared with internal/api. Both
// packages take this same Postgres advisory lock so their test runs cannot
// overlap on a shared TEST_DATABASE_URL. Keep the two values identical.
const testDBAdvisoryLock = 0x0170_9105

const (
	testURLA = "http://pg6mmjiyjmcrsslvykfwnntlaru7p5svn6y2ymmju6nubxndf4pscryd.onion/"
	testURLB = "http://sp3k262uwy4r2k3ycr5awluarykdpag6a7y33jxop4cs2lu5uz5sseqd.onion/"
	testURLC = "http://xa4r2iadxm55fbnqgwwi5mymqdcofiu3w6rpbtqn7b2dyn7mgwj64jyd.onion/"
)

func lockSharedTestDatabase(t *testing.T, conn *sql.DB) {
	t.Helper()
	lockCtx := context.Background()
	lockConn, err := conn.Conn(lockCtx)
	if err != nil {
		t.Fatalf("checkout lock connection: %v", err)
	}
	if _, err := lockConn.ExecContext(lockCtx, `SELECT pg_advisory_lock($1)`, testDBAdvisoryLock); err != nil {
		lockConn.Close()
		t.Fatalf("acquire test-db advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = lockConn.ExecContext(lockCtx, `SELECT pg_advisory_unlock($1)`, testDBAdvisoryLock)
		lockConn.Close()
	})
}

// newTestDB returns a *DB backed by a throwaway Postgres at $TEST_DATABASE_URL,
// freshly migrated and truncated. Skips when the env var is unset so the
// default `go test ./...` stays hermetic. CI provides a postgres service.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping postgres integration test")
	}
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := conn.Ping(); err != nil {
		t.Fatalf("db.Ping: %v", err)
	}
	if err := runMigrations(conn); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	// `go test ./...` runs this package and internal/api concurrently against
	// the same TEST_DATABASE_URL, and both TRUNCATE — so each was wiping the
	// other's fixtures mid-test. The advisory lock makes them take turns. See
	// the longer note on lockTestDB in internal/api/integration_test.go for why
	// it is held on a dedicated connection and released explicitly.
	lockSharedTestDatabase(t, conn)
	if _, err := conn.Exec(`TRUNCATE nodes, edges, auth_audit, blacklist, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return &DB{Conn: conn}
}

// mustUser creates a verified user and returns its ID.
func mustUser(t *testing.T, db *DB, email string) int {
	t.Helper()
	if err := db.CreateUser(email, "x", "user", ""); err != nil {
		t.Fatalf("CreateUser(%s): %v", email, err)
	}
	u, err := db.GetUserByEmail(email)
	if err != nil || u == nil {
		t.Fatalf("GetUserByEmail(%s): %v", email, err)
	}
	return u.ID
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestGetNextPendingNode_FairAcrossUsers proves the scheduler does not let a
// user with a large backlog starve another user: with user A holding 4 pending
// nodes and user B holding 1, the first two claims (left in-flight, simulating
// busy workers) must service BOTH users — then, once B is exhausted, remaining
// claims fall through to A (work-conserving). 4 + 1 = 5 nodes total.
func TestGetNextPendingNode_FairAcrossUsers(t *testing.T) {
	db := newTestDB(t)
	a := mustUser(t, db, "a@example.com")
	b := mustUser(t, db, "b@example.com")

	for i := 0; i < 4; i++ {
		url := testURLA + string(rune('a'+i))
		if err := db.EnqueueURL(url, 0, a, 0); err != nil {
			t.Fatalf("enqueue A: %v", err)
		}
	}
	if err := db.EnqueueURL(testURLB, 0, b, 0); err != nil {
		t.Fatalf("enqueue B: %v", err)
	}

	// Claim twice without completing — both stay 'crawling'.
	var claims []int
	for i := 0; i < 2; i++ {
		url, _, uid, err := db.GetNextPendingNode()
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if url == "" {
			t.Fatalf("claim %d: empty (expected work available)", i)
		}
		claims = append(claims, uid)
	}
	if !contains(claims, a) || !contains(claims, b) {
		t.Errorf("first two claims should service both users (fairness), got user ids %v", claims)
	}

	// B is now exhausted (its single node is in-flight). The next three claims
	// must all be A's remaining pending nodes — proving work-conservation.
	for i := 0; i < 3; i++ {
		url, _, uid, err := db.GetNextPendingNode()
		if err != nil {
			t.Fatalf("followup claim %d: %v", i, err)
		}
		if url == "" || uid != a {
			t.Fatalf("followup claim %d: got (url=%q, uid=%d), want one of A's pending nodes", i, url, uid)
		}
	}

	// All 6 nodes are now in-flight; the queue is empty.
	if url, _, _, err := db.GetNextPendingNode(); err != nil || url != "" {
		t.Errorf("expected empty queue, got url=%q err=%v", url, err)
	}
}

// TestGetNextPendingNode_PerTenantClaim guards the (url, user_id) match: two
// users tracking the SAME url must be claimed independently — claiming one must
// not flip the other tenant's copy to 'crawling'.
func TestGetNextPendingNode_PerTenantClaim(t *testing.T) {
	db := newTestDB(t)
	a := mustUser(t, db, "a@example.com")
	b := mustUser(t, db, "b@example.com")

	const shared = testURLC
	if err := db.EnqueueURL(shared, 0, a, 0); err != nil {
		t.Fatalf("enqueue A: %v", err)
	}
	if err := db.EnqueueURL(shared, 0, b, 0); err != nil {
		t.Fatalf("enqueue B: %v", err)
	}

	url, _, firstUID, err := db.GetNextPendingNode()
	if err != nil || url != shared {
		t.Fatalf("first claim: url=%q uid=%d err=%v", url, firstUID, err)
	}

	// Exactly one tenant's copy should remain 'pending'.
	var pending int
	if err := db.Conn.QueryRow(
		`SELECT COUNT(*) FROM nodes WHERE url=$1 AND processing_status='pending'`, shared,
	).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 1 {
		t.Errorf("after claiming one tenant's copy, want 1 still pending, got %d", pending)
	}

	// The second claim must return the OTHER tenant.
	url2, _, secondUID, err := db.GetNextPendingNode()
	if err != nil || url2 != shared {
		t.Fatalf("second claim: url=%q err=%v", url2, err)
	}
	if secondUID == firstUID {
		t.Errorf("both claims returned the same tenant (%d) — (url,user_id) match is broken", firstUID)
	}
}

func TestRootURLWithoutSlashDoesNotDuplicateAfterSave(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "root-form@example.com")
	rootWithoutSlash := strings.TrimSuffix(testURLA, "/")
	if err := db.EnqueueURL(rootWithoutSlash, 0, uid, 0); err != nil {
		t.Fatalf("EnqueueURL: %v", err)
	}
	claimed, _, claimedUID, err := db.GetNextPendingNode()
	if err != nil || claimed != rootWithoutSlash || claimedUID != uid {
		t.Fatalf("claim = (%q,%d,%v), want (%q,%d,nil)", claimed, claimedUID, err, rootWithoutSlash, uid)
	}
	if _, err := db.SaveNode(claimed, "root", "", 200, "completed", "{}", "content", "wiki", uid); err != nil {
		t.Fatalf("SaveNode: %v", err)
	}
	var rows int
	if err := db.Conn.QueryRow(`SELECT COUNT(*) FROM nodes WHERE user_id=$1`, uid).Scan(&rows); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if rows != 1 {
		t.Fatalf("root URL changed identity during save: got %d rows, want 1", rows)
	}
}

func TestGetStats_AdminSeesAllPendingNodes(t *testing.T) {
	db := newTestDB(t)
	admin := mustUser(t, db, "admin@example.com")
	user := mustUser(t, db, "user@example.com")

	if err := db.EnqueueURL(testURLA, 0, admin, 0); err != nil {
		t.Fatalf("enqueue admin: %v", err)
	}
	if err := db.EnqueueURL(testURLB, 0, user, 0); err != nil {
		t.Fatalf("enqueue user: %v", err)
	}

	adminStats, err := db.GetStats(admin, true)
	if err != nil {
		t.Fatalf("GetStats admin: %v", err)
	}
	if adminStats.PendingNodes != 2 {
		t.Fatalf("admin pending nodes = %d, want 2", adminStats.PendingNodes)
	}

	userStats, err := db.GetStats(user, false)
	if err != nil {
		t.Fatalf("GetStats user: %v", err)
	}
	if userStats.PendingNodes != 1 {
		t.Fatalf("user pending nodes = %d, want 1", userStats.PendingNodes)
	}
}

func TestGraphMLEdgesNeverCrossTenantNodeIDs(t *testing.T) {
	db := newTestDB(t)
	a := mustUser(t, db, "graph-a@example.com")
	b := mustUser(t, db, "graph-b@example.com")

	for _, uid := range []int{a, b} {
		if _, err := db.SaveNode(testURLA, "source", "", 200, "completed", "{}", "source", "wiki", uid); err != nil {
			t.Fatalf("SaveNode source for user %d: %v", uid, err)
		}
		if _, err := db.SaveNode(testURLB, "target", "", 200, "completed", "{}", "target", "wiki", uid); err != nil {
			t.Fatalf("SaveNode target for user %d: %v", uid, err)
		}
		if err := db.SaveEdge(testURLA, testURLB, 1, uid); err != nil {
			t.Fatalf("SaveEdge for user %d: %v", uid, err)
		}
	}

	ids := func(uid int, rawURL string) int {
		t.Helper()
		var id int
		if err := db.Conn.QueryRow(`SELECT id FROM nodes WHERE user_id=$1 AND url=$2`, uid, rawURL).Scan(&id); err != nil {
			t.Fatalf("node id for user %d: %v", uid, err)
		}
		return id
	}
	aSource, aTarget := ids(a, testURLA), ids(a, testURLB)
	bSource, bTarget := ids(b, testURLA), ids(b, testURLB)

	var userEdges []GraphMLEdge
	if err := db.ExportGraphMLEdges(context.Background(), a, false, func(edge GraphMLEdge) error {
		userEdges = append(userEdges, edge)
		return nil
	}); err != nil {
		t.Fatalf("user GraphML edges: %v", err)
	}
	if len(userEdges) != 1 || userEdges[0] != (GraphMLEdge{SourceID: aSource, TargetID: aTarget}) {
		t.Fatalf("user export crossed tenant boundary: %+v", userEdges)
	}

	allowed := map[GraphMLEdge]bool{
		{SourceID: aSource, TargetID: aTarget}: true,
		{SourceID: bSource, TargetID: bTarget}: true,
	}
	var adminEdges []GraphMLEdge
	if err := db.ExportGraphMLEdges(context.Background(), a, true, func(edge GraphMLEdge) error {
		adminEdges = append(adminEdges, edge)
		return nil
	}); err != nil {
		t.Fatalf("admin GraphML edges: %v", err)
	}
	if len(adminEdges) != len(allowed) {
		t.Fatalf("admin export returned %d edges, want %d: %+v", len(adminEdges), len(allowed), adminEdges)
	}
	for _, edge := range adminEdges {
		if !allowed[edge] {
			t.Fatalf("admin export synthesized a cross-tenant edge: %+v", edge)
		}
	}
}

func TestOnionValidationAndDomainWideBlacklist(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "queue@example.com")

	badChecksum := "http://ag6mmjiyjmcrsslvykfwnntlaru7p5svn6y2ymmju6nubxndf4pscryd.onion/"
	if err := db.EnqueueURL(badChecksum, 0, uid, 0); !errors.Is(err, ErrInvalidOnionURL) {
		t.Fatalf("bad checksum enqueue error = %v, want ErrInvalidOnionURL", err)
	}
	if err := db.EnqueueURL("http://user:secret@"+strings.TrimPrefix(testURLA, "http://"), 0, uid, 0); !errors.Is(err, ErrInvalidOnionURL) {
		t.Fatalf("userinfo enqueue error = %v, want ErrInvalidOnionURL", err)
	}

	host := strings.TrimSuffix(strings.TrimPrefix(testURLA, "http://"), "/")
	if err := db.AddBlacklist(host + ":443"); err != nil {
		t.Fatalf("AddBlacklist canonical port: %v", err)
	}
	blocked, err := db.IsDomainBlacklisted(host + ":80")
	if err != nil || !blocked {
		t.Fatalf("domain-wide blacklist lookup: blocked=%v err=%v", blocked, err)
	}
	if err := db.EnqueueURL(testURLA+"private", 0, uid, 0); !errors.Is(err, ErrBlacklisted) {
		t.Fatalf("blacklisted enqueue error = %v, want ErrBlacklisted", err)
	}

	// A simultaneous enqueue and block must never leave a crawlable row behind,
	// regardless of which transaction acquires the per-domain lock first.
	hostC := strings.TrimSuffix(strings.TrimPrefix(testURLC, "http://"), "/")
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		results <- db.EnqueueURL(testURLC, 0, uid, 0)
	}()
	go func() {
		<-start
		results <- db.AddBlacklist(hostC)
	}()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil && !errors.Is(err, ErrBlacklisted) {
			t.Fatalf("concurrent blacklist/enqueue: %v", err)
		}
	}
	var crawlable int
	if err := db.Conn.QueryRow(`
		SELECT COUNT(*) FROM nodes WHERE user_id=$1 AND url=$2 AND processing_status != 'blocked'
	`, uid, testURLC).Scan(&crawlable); err != nil {
		t.Fatalf("count crawlable rows: %v", err)
	}
	if crawlable != 0 {
		t.Fatal("enqueue/blacklist race left a crawlable node behind")
	}
}

func TestCreateRegisteredUserBootstrapsOnlyOneAdmin(t *testing.T) {
	db := newTestDB(t)
	emails := []string{"bootstrap-a@example.com", "bootstrap-b@example.com"}
	errCh := make(chan error, len(emails))
	var wg sync.WaitGroup
	for _, emailAddress := range emails {
		emailAddress := emailAddress
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := db.CreateRegisteredUser(emailAddress, "hash", "token-"+emailAddress, emailAddress)
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("CreateRegisteredUser: %v", err)
		}
	}

	var admins, users int
	if err := db.Conn.QueryRow(`
		SELECT COUNT(*) FILTER (WHERE role='admin'), COUNT(*) FILTER (WHERE role='user')
		FROM users WHERE email IN ($1, $2)
	`, emails[0], emails[1]).Scan(&admins, &users); err != nil {
		t.Fatalf("count bootstrap roles: %v", err)
	}
	if admins != 1 || users != 1 {
		t.Fatalf("bootstrap roles: admins=%d users=%d, want 1 and 1", admins, users)
	}
	if _, err := db.CreateRegisteredUser(emails[0], "hash", "new-token", emails[0]); !errors.Is(err, ErrEmailInUse) {
		t.Fatalf("duplicate registration error = %v, want ErrEmailInUse", err)
	}
}

func TestPasswordReset_Flow(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "u@example.com")

	_, _, v0, found, err := db.GetUserAuthInfo(uid)
	if err != nil || !found {
		t.Fatalf("GetUserAuthInfo: found=%v err=%v", found, err)
	}

	const tok = "resettoken1234567890abcdef"
	ok, err := db.SetResetToken("u@example.com", tok)
	if err != nil || !ok {
		t.Fatalf("SetResetToken: ok=%v err=%v", ok, err)
	}
	var stored string
	if err := db.Conn.QueryRow(`SELECT reset_token FROM users WHERE id=$1`, uid).Scan(&stored); err != nil {
		t.Fatalf("read stored reset credential: %v", err)
	}
	if stored == tok || stored != opaqueTokenHash(tok) {
		t.Fatal("reset credential was not persisted exclusively as its digest")
	}

	if err := db.ResetPassword(tok, "newhash"); err != nil {
		t.Fatalf("ResetPassword (valid token): %v", err)
	}

	// token_version must be bumped (all sessions revoked).
	_, _, v1, _, _ := db.GetUserAuthInfo(uid)
	if v1 != v0+1 {
		t.Errorf("token_version not bumped: %d -> %d", v0, v1)
	}

	// Password hash must be the new one.
	u, _ := db.GetUserByEmail("u@example.com")
	if u == nil || u.PasswordHash != "newhash" {
		t.Errorf("password hash not updated: %+v", u)
	}

	// Reusing the same token must fail.
	if err := db.ResetPassword(tok, "another"); err == nil {
		t.Error("reused reset token was accepted")
	}
}

func TestEmailVerificationTokenIsStoredHashed(t *testing.T) {
	db := newTestDB(t)
	const (
		emailAddress = "verify@example.com"
		token        = "verifytoken1234567890abcdef"
	)
	if err := db.CreateUser(emailAddress, "hash", "user", token); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	var stored string
	if err := db.Conn.QueryRow(`SELECT verification_token FROM users WHERE email=$1`, emailAddress).Scan(&stored); err != nil {
		t.Fatalf("read stored verification credential: %v", err)
	}
	if stored == token || stored != opaqueTokenHash(token) {
		t.Fatal("verification credential was not persisted exclusively as its digest")
	}
	if err := db.VerifyUser(token); err != nil {
		t.Fatalf("VerifyUser with original token: %v", err)
	}
	if err := db.VerifyUser(token); err == nil {
		t.Fatal("single-use verification token was accepted twice")
	}
}

func TestOpaqueTokenDataMigration(t *testing.T) {
	db := newTestDB(t)
	const (
		emailAddress = "legacy@example.com"
		verifyToken  = "legacy-plaintext-verification-token"
		resetToken   = "legacy-plaintext-reset-token"
	)
	if _, err := db.Conn.Exec(`DELETE FROM security_data_migrations WHERE name=$1`, opaqueTokenMigration); err != nil {
		t.Fatalf("reset data-migration marker: %v", err)
	}
	if _, err := db.Conn.Exec(`
		INSERT INTO users (email, password_hash, verification_token, reset_token, reset_expires_at)
		VALUES ($1, 'hash', $2, $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
	`, emailAddress, verifyToken, resetToken); err != nil {
		t.Fatalf("insert legacy credentials: %v", err)
	}

	if err := migrateOpaqueTokens(db.Conn); err != nil {
		t.Fatalf("migrateOpaqueTokens: %v", err)
	}
	var storedVerify, storedReset string
	if err := db.Conn.QueryRow(`
		SELECT verification_token, reset_token FROM users WHERE email=$1
	`, emailAddress).Scan(&storedVerify, &storedReset); err != nil {
		t.Fatalf("read migrated credentials: %v", err)
	}
	if storedVerify != opaqueTokenHash(verifyToken) || storedReset != opaqueTokenHash(resetToken) {
		t.Fatal("legacy credentials were not replaced with their SHA-256 digests")
	}

	// The marker makes subsequent startups idempotent; hashing the digest a
	// second time would invalidate every outstanding link.
	if err := migrateOpaqueTokens(db.Conn); err != nil {
		t.Fatalf("second migrateOpaqueTokens: %v", err)
	}
	var afterSecondRun string
	if err := db.Conn.QueryRow(`SELECT verification_token FROM users WHERE email=$1`, emailAddress).Scan(&afterSecondRun); err != nil {
		t.Fatalf("read credential after second run: %v", err)
	}
	if afterSecondRun != storedVerify {
		t.Fatal("idempotent data migration hashed an existing digest again")
	}
}

func TestPasswordReset_Expired(t *testing.T) {
	db := newTestDB(t)
	mustUser(t, db, "u@example.com")

	const tok = "expiredtoken1234567890abcdef"
	if _, err := db.SetResetToken("u@example.com", tok); err != nil {
		t.Fatalf("SetResetToken: %v", err)
	}
	// Force the token into the past.
	if _, err := db.Conn.Exec(
		`UPDATE users SET reset_expires_at = CURRENT_TIMESTAMP - INTERVAL '1 hour' WHERE reset_token=$1`, opaqueTokenHash(tok),
	); err != nil {
		t.Fatalf("expire token: %v", err)
	}
	if err := db.ResetPassword(tok, "x"); err == nil {
		t.Error("expired reset token was accepted")
	}
}

func TestSetResetToken_UnknownEmailNotFound(t *testing.T) {
	db := newTestDB(t)
	found, err := db.SetResetToken("nobody@example.com", "tok1234567890abcdef")
	if err != nil {
		t.Fatalf("SetResetToken: %v", err)
	}
	if found {
		t.Error("SetResetToken reported found=true for a non-existent email")
	}
}

// mustNode inserts a node owned by userID in the given state, with next_crawl_at
// pushed `ageDays` into the past.
func mustNode(t *testing.T, db *DB, url string, userID int, status string, ageDays int) {
	t.Helper()
	_, err := db.Conn.Exec(`
		INSERT INTO nodes (url, user_id, processing_status, retry_count, next_crawl_at, depth)
		VALUES ($1, $2, $3, 5, CURRENT_TIMESTAMP - ($4 || ' days')::INTERVAL, 0)
	`, url, userID, status, ageDays)
	if err != nil {
		t.Fatalf("insert node %s: %v", url, err)
	}
}

func nodeState(t *testing.T, db *DB, url string) (string, int) {
	t.Helper()
	var status string
	var retries int
	if err := db.Conn.QueryRow(
		`SELECT processing_status, retry_count FROM nodes WHERE url=$1`, url,
	).Scan(&status, &retries); err != nil {
		t.Fatalf("read node %s: %v", url, err)
	}
	return status, retries
}

// A queue-wide outage leaves every node 'failed' with retries exhausted, and
// 'failed' is terminal for the scheduler — so without revival the crawler stays
// idle forever after the cause is fixed.
func TestReviveFailedNodes_ReturnsStaleFailuresToQueue(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "u@example.com")
	mustNode(t, db, "http://old.onion/", uid, "failed", 30)

	n, err := db.ReviveFailedNodes(7*24*time.Hour, 100)
	if err != nil {
		t.Fatalf("ReviveFailedNodes: %v", err)
	}
	if n != 1 {
		t.Fatalf("revived %d nodes, want 1", n)
	}
	status, retries := nodeState(t, db, "http://old.onion/")
	if status != "pending" {
		t.Errorf("status = %q, want pending", status)
	}
	// The retry budget has to be reset too, or the node fails once and is
	// immediately terminal again.
	if retries != 0 {
		t.Errorf("retry_count = %d, want 0", retries)
	}
}

func TestReviveFailedNodes_LeavesRecentFailuresAlone(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "u@example.com")
	mustNode(t, db, "http://recent.onion/", uid, "failed", 1)

	n, err := db.ReviveFailedNodes(7*24*time.Hour, 100)
	if err != nil {
		t.Fatalf("ReviveFailedNodes: %v", err)
	}
	if n != 0 {
		t.Fatalf("revived %d nodes, want 0 — a 1-day-old failure is still inside its backoff", n)
	}
}

// 'blocked' means robots.txt or the blacklist refused the URL deliberately.
// Reviving those would re-crawl something the operator or the site opted out of.
func TestReviveFailedNodes_NeverRevivesBlocked(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "u@example.com")
	mustNode(t, db, "http://blocked.onion/", uid, "blocked", 90)

	n, err := db.ReviveFailedNodes(7*24*time.Hour, 100)
	if err != nil {
		t.Fatalf("ReviveFailedNodes: %v", err)
	}
	if n != 0 {
		t.Fatalf("revived %d blocked nodes, want 0", n)
	}
	if status, _ := nodeState(t, db, "http://blocked.onion/"); status != "blocked" {
		t.Errorf("status = %q, want blocked to be untouched", status)
	}
}

// A backlog of thousands arriving at once would hit the per-domain politeness
// delay across every host simultaneously, so revival is batched.
func TestReviveFailedNodes_RespectsBatchLimit(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "u@example.com")
	for i := 0; i < 5; i++ {
		mustNode(t, db, fmt.Sprintf("http://n%d.onion/", i), uid, "failed", 30)
	}
	n, err := db.ReviveFailedNodes(7*24*time.Hour, 2)
	if err != nil {
		t.Fatalf("ReviveFailedNodes: %v", err)
	}
	if n != 2 {
		t.Fatalf("revived %d nodes, want exactly the batch limit of 2", n)
	}
}

func TestEnqueueURLEnforcesThePerUserQueueQuota(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "quota@example.com")

	if err := db.EnqueueURL(testURLA, 0, uid, 2); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if err := db.EnqueueURL(testURLB, 0, uid, 2); err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if err := db.EnqueueURL(testURLC, 0, uid, 2); !errors.Is(err, ErrQueueQuotaExceeded) {
		t.Fatalf("third enqueue past the quota: got %v, want ErrQueueQuotaExceeded", err)
	}

	pending, err := db.CountPendingNodes(uid)
	if err != nil {
		t.Fatalf("CountPendingNodes: %v", err)
	}
	if pending != 2 {
		t.Fatalf("quota let %d URLs through, want 2", pending)
	}
}

func TestQueueQuotaIsPerAccount(t *testing.T) {
	// One tenant filling its queue must not stop another from crawling at all.
	db := newTestDB(t)
	full := mustUser(t, db, "full@example.com")
	other := mustUser(t, db, "other@example.com")

	if err := db.EnqueueURL(testURLA, 0, full, 1); err != nil {
		t.Fatalf("filling first account: %v", err)
	}
	if err := db.EnqueueURL(testURLB, 0, full, 1); !errors.Is(err, ErrQueueQuotaExceeded) {
		t.Fatalf("first account should be at quota: %v", err)
	}
	if err := db.EnqueueURL(testURLB, 0, other, 1); err != nil {
		t.Fatalf("second account was blocked by the first account's queue: %v", err)
	}
}

func TestQueueQuotaHoldsUnderConcurrentSubmissions(t *testing.T) {
	// The count and the insert share one transaction behind a per-account lock,
	// so parallel submissions cannot each see the same free slot and both take
	// it. The URLs deliberately span three different hosts: the per-domain lock
	// does not serialize those, so this test fails if the account lock is
	// missing. A release barrier makes the submissions actually overlap rather
	// than relying on goroutine scheduling to collide.
	db := newTestDB(t)
	uid := mustUser(t, db, "race@example.com")

	urls := []string{testURLA, testURLB, testURLC}
	const quota = 2

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, len(urls))
	for _, u := range urls {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			<-start
			errs <- db.EnqueueURL(u, 0, uid, quota)
		}(u)
	}
	close(start)
	wg.Wait()
	close(errs)

	var accepted, rejected int
	for err := range errs {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrQueueQuotaExceeded):
			rejected++
		default:
			t.Fatalf("unexpected enqueue error: %v", err)
		}
	}
	if accepted > quota {
		t.Fatalf("concurrent submissions overran the quota: %d accepted, quota %d", accepted, quota)
	}
	pending, err := db.CountPendingNodes(uid)
	if err != nil {
		t.Fatalf("CountPendingNodes: %v", err)
	}
	if pending > quota {
		t.Fatalf("queue holds %d pending URLs, quota %d", pending, quota)
	}
	if accepted+rejected != len(urls) {
		t.Fatalf("accounted for %d of %d submissions", accepted+rejected, len(urls))
	}
}

func TestEnqueueURLWithoutAQuotaIsUnbounded(t *testing.T) {
	// The crawler's own discovery path passes zero; it must stay uncapped so
	// MaxDepth remains the only bound on how far a single crawl reaches.
	db := newTestDB(t)
	uid := mustUser(t, db, "nolimit@example.com")
	for _, u := range []string{testURLA, testURLB, testURLC} {
		if err := db.EnqueueURL(u, 0, uid, 0); err != nil {
			t.Fatalf("enqueue %s with quota disabled: %v", u, err)
		}
	}
}
