package database

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"
)

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
		url := "http://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaad.onion/" + string(rune('a'+i))
		if err := db.EnqueueURL(url, 0, a); err != nil {
			t.Fatalf("enqueue A: %v", err)
		}
	}
	if err := db.EnqueueURL("http://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbd.onion/", 0, b); err != nil {
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

	const shared = "http://cccccccccccccccccccccccccccccccccccccccccccccccccccccccd.onion/"
	if err := db.EnqueueURL(shared, 0, a); err != nil {
		t.Fatalf("enqueue A: %v", err)
	}
	if err := db.EnqueueURL(shared, 0, b); err != nil {
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

func TestGetStats_AdminSeesAllPendingNodes(t *testing.T) {
	db := newTestDB(t)
	admin := mustUser(t, db, "admin@example.com")
	user := mustUser(t, db, "user@example.com")

	if err := db.EnqueueURL("http://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaad.onion/", 0, admin); err != nil {
		t.Fatalf("enqueue admin: %v", err)
	}
	if err := db.EnqueueURL("http://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbd.onion/", 0, user); err != nil {
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

func TestPasswordReset_Flow(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "u@example.com")

	_, v0, found, err := db.GetUserAuthInfo(uid)
	if err != nil || !found {
		t.Fatalf("GetUserAuthInfo: found=%v err=%v", found, err)
	}

	const tok = "resettoken1234567890abcdef"
	ok, err := db.SetResetToken("u@example.com", tok)
	if err != nil || !ok {
		t.Fatalf("SetResetToken: ok=%v err=%v", ok, err)
	}

	if err := db.ResetPassword(tok, "newhash"); err != nil {
		t.Fatalf("ResetPassword (valid token): %v", err)
	}

	// token_version must be bumped (all sessions revoked).
	_, v1, _, _ := db.GetUserAuthInfo(uid)
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

func TestPasswordReset_Expired(t *testing.T) {
	db := newTestDB(t)
	mustUser(t, db, "u@example.com")

	const tok = "expiredtoken1234567890abcdef"
	if _, err := db.SetResetToken("u@example.com", tok); err != nil {
		t.Fatalf("SetResetToken: %v", err)
	}
	// Force the token into the past.
	if _, err := db.Conn.Exec(
		`UPDATE users SET reset_expires_at = CURRENT_TIMESTAMP - INTERVAL '1 hour' WHERE reset_token=$1`, tok,
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
