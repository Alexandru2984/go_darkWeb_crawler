package database

import (
	"database/sql"
	"os"
	"testing"
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
	if !(contains(claims, a) && contains(claims, b)) {
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
