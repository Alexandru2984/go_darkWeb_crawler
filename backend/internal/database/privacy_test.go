package database

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mustAgedNode inserts a completed node whose discovery and last-crawl
// timestamps are both `ageDays` in the past — the shape the retention sweeper
// actually selects on. mustNode ages next_crawl_at instead, which is what the
// revival tests need and what retention deliberately ignores.
func mustAgedNode(t *testing.T, db *DB, url string, userID int, ageDays int) {
	t.Helper()
	_, err := db.Conn.Exec(`
		INSERT INTO nodes (url, user_id, processing_status, content, discovered_at, last_crawled_at)
		VALUES ($1, $2, 'completed', 'page text',
		        CURRENT_TIMESTAMP - ($3 || ' days')::INTERVAL,
		        CURRENT_TIMESTAMP - ($3 || ' days')::INTERVAL)
	`, url, userID, ageDays)
	if err != nil {
		t.Fatalf("insert aged node %s: %v", url, err)
	}
}

func nodeCount(t *testing.T, db *DB, userID int) int {
	t.Helper()
	var n int
	if err := db.Conn.QueryRow(`SELECT COUNT(*) FROM nodes WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	return n
}

func setRetention(t *testing.T, db *DB, userID, days int) {
	t.Helper()
	if err := db.SetPrivacySettings(userID, days, true); err != nil {
		t.Fatalf("SetPrivacySettings(%d, %d): %v", userID, days, err)
	}
}

// Retention is a per-account policy. An account that never set one must not
// lose records because another account did — the sweeper runs globally, so
// getting this wrong destroys data belonging to people who never opted in.
func TestRetentionAppliesOnlyToAccountsThatSetAWindow(t *testing.T) {
	db := newTestDB(t)
	withPolicy := mustUser(t, db, "policy@example.com")
	noPolicy := mustUser(t, db, "nopolicy@example.com")
	setRetention(t, db, withPolicy, 30)

	mustAgedNode(t, db, testURLA, withPolicy, 60)
	mustAgedNode(t, db, testURLB, withPolicy, 5)
	mustAgedNode(t, db, testURLC, noPolicy, 500)

	report, err := db.ApplyRetention(context.Background(), 100, false)
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if report.NodesDeleted != 1 {
		t.Errorf("deleted %d nodes, want exactly the one past the window", report.NodesDeleted)
	}
	if got := nodeCount(t, db, withPolicy); got != 1 {
		t.Errorf("account with a 30-day window kept %d nodes, want 1 (the recent one)", got)
	}
	if got := nodeCount(t, db, noPolicy); got != 1 {
		t.Errorf("account without a retention policy lost records: %d remain, want 1", got)
	}
}

// Dry run is how a destructive timer is introduced to a database that already
// holds data. If it deleted anything, that safety net would be worse than not
// having one — the operator would believe they were only observing.
func TestRetentionDryRunReportsWithoutDeleting(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "dry@example.com")
	setRetention(t, db, uid, 7)
	mustAgedNode(t, db, testURLA, uid, 60)
	mustAgedNode(t, db, testURLB, uid, 90)

	report, err := db.ApplyRetention(context.Background(), 100, true)
	if err != nil {
		t.Fatalf("ApplyRetention(dry): %v", err)
	}
	if !report.DryRun {
		t.Error("report does not identify itself as a dry run")
	}
	if report.NodesMatched != 2 {
		t.Errorf("matched %d, want 2", report.NodesMatched)
	}
	if report.NodesDeleted != 0 {
		t.Errorf("dry run deleted %d nodes, want 0", report.NodesDeleted)
	}
	if got := nodeCount(t, db, uid); got != 2 {
		t.Errorf("dry run left %d nodes, want 2 untouched", got)
	}
}

// A node in 'crawling' is held by a worker that will write results back to it.
// Deleting it mid-flight produces a completed crawl with nowhere to land.
func TestRetentionLeavesInFlightCrawlsAlone(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "inflight@example.com")
	setRetention(t, db, uid, 1)
	mustAgedNode(t, db, testURLA, uid, 30)
	if _, err := db.Conn.Exec(
		`UPDATE nodes SET processing_status = 'crawling' WHERE url = $1`, testURLA); err != nil {
		t.Fatalf("mark crawling: %v", err)
	}

	report, err := db.ApplyRetention(context.Background(), 100, false)
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if report.NodesDeleted != 0 {
		t.Errorf("deleted %d in-flight nodes, want 0", report.NodesDeleted)
	}
	if got := nodeCount(t, db, uid); got != 1 {
		t.Errorf("in-flight node was removed: %d remain, want 1", got)
	}
}

// The batch bounds one account's deletions per pass, so a large backlog drains
// over several passes rather than holding one enormous delete open.
func TestRetentionRespectsTheBatchBound(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "batch@example.com")
	setRetention(t, db, uid, 1)
	for i := 0; i < 5; i++ {
		mustAgedNode(t, db, testURLA+string(rune('a'+i)), uid, 30)
	}

	report, err := db.ApplyRetention(context.Background(), 2, false)
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if report.NodesDeleted != 2 {
		t.Errorf("deleted %d nodes in one pass, want the 2 the batch allows", report.NodesDeleted)
	}
	if got := nodeCount(t, db, uid); got != 3 {
		t.Errorf("%d nodes remain, want 3", got)
	}
}

// Deleting an account has to take everything with it. auth_audit in particular
// is keyed by a reference of the address rather than by user_id, so it is
// invisible to the foreign-key cascade — an account "deleted" with its login
// history still in the table has not really been deleted.
func TestAccountDeletionRemovesEveryTraceOfTheAccount(t *testing.T) {
	db := newTestDB(t)
	doomed := mustUser(t, db, "doomed@example.com")
	bystander := mustUser(t, db, "bystander@example.com")

	mustAgedNode(t, db, testURLA, doomed, 1)
	mustAgedNode(t, db, testURLB, doomed, 1)
	mustAgedNode(t, db, testURLC, bystander, 1)
	if err := db.SaveEdge(testURLA, testURLB, 1, doomed); err != nil {
		t.Fatalf("SaveEdge: %v", err)
	}
	if _, err := db.CreateSession(doomed, "tok-doomed", "Firefox on Linux", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := db.CreateSession(bystander, "tok-bystander", "Firefox on Linux", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession(bystander): %v", err)
	}
	if err := db.StageTOTPSecret(doomed, "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatalf("StageTOTPSecret: %v", err)
	}
	if err := db.ConfirmTOTP(doomed, 1, []string{RecoveryCodeHash("aaaa-aaaaa")}); err != nil {
		t.Fatalf("ConfirmTOTP: %v", err)
	}
	db.LogAuthEvent("login_ok", "ref-doomed", "ip-ref")
	db.LogAuthEvent("login_ok", "ref-bystander", "ip-ref")

	if err := db.DeleteAccount(doomed, "ref-doomed"); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}

	for _, c := range []struct {
		what  string
		query string
	}{
		{"user row", `SELECT COUNT(*) FROM users WHERE id = $1`},
		{"nodes", `SELECT COUNT(*) FROM nodes WHERE user_id = $1`},
		{"edges", `SELECT COUNT(*) FROM edges WHERE user_id = $1`},
		{"sessions", `SELECT COUNT(*) FROM sessions WHERE user_id = $1`},
		{"recovery codes", `SELECT COUNT(*) FROM recovery_codes WHERE user_id = $1`},
	} {
		var n int
		if err := db.Conn.QueryRow(c.query, doomed).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", c.what, err)
		}
		if n != 0 {
			t.Errorf("%s survived account deletion: %d rows remain", c.what, n)
		}
	}

	var audit int
	if err := db.Conn.QueryRow(
		`SELECT COUNT(*) FROM auth_audit WHERE email = 'ref-doomed'`).Scan(&audit); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if audit != 0 {
		t.Errorf("login history survived account deletion: %d rows remain", audit)
	}

	// The bystander is the point of the test: a cascade that reaches too far is
	// as much a failure as one that does not reach far enough.
	if got := nodeCount(t, db, bystander); got != 1 {
		t.Errorf("another account's nodes were deleted: %d remain, want 1", got)
	}
	var others int
	if err := db.Conn.QueryRow(
		`SELECT COUNT(*) FROM auth_audit WHERE email = 'ref-bystander'`).Scan(&others); err != nil {
		t.Fatalf("count bystander audit: %v", err)
	}
	if others != 1 {
		t.Errorf("another account's login history was deleted: %d remain, want 1", others)
	}
}

// The grace period is the whole protection. A second request that reset the
// clock would be harmless, but one that moved the deadline *closer* would not —
// so the request is refused outright while one is pending, and the original
// deadline stands until the owner cancels it.
func TestSecondDeletionRequestDoesNotMoveTheDeadline(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "twice@example.com")

	first, err := db.RequestAccountDeletion(uid, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("first RequestAccountDeletion: %v", err)
	}
	if _, err := db.RequestAccountDeletion(uid, time.Second); !errors.Is(err, ErrDeletionPending) {
		t.Fatalf("second request returned %v, want ErrDeletionPending", err)
	}

	settings, err := db.GetPrivacySettings(uid)
	if err != nil {
		t.Fatalf("GetPrivacySettings: %v", err)
	}
	if settings.DeletionScheduledFor == nil {
		t.Fatal("deletion is no longer scheduled after a refused second request")
	}
	if !settings.DeletionScheduledFor.Equal(first) {
		t.Errorf("deadline moved from %v to %v", first, *settings.DeletionScheduledFor)
	}
}

// Cancelling has to be complete: an account left with a deletion timestamp the
// sweeper can still see would be erased days after the owner was told it would
// not be.
func TestCancellingDeletionClearsItFromTheSweeper(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "cancel@example.com")

	// A window already in the past, so the account is due immediately and the
	// test proves the cancellation rather than a not-yet-elapsed timer.
	if _, err := db.RequestAccountDeletion(uid, 0); err != nil {
		t.Fatalf("RequestAccountDeletion: %v", err)
	}
	due, err := db.DueAccountDeletions(10)
	if err != nil {
		t.Fatalf("DueAccountDeletions: %v", err)
	}
	if len(due) != 1 || due[0].UserID != uid {
		t.Fatalf("account is not due for deletion: %+v", due)
	}

	cancelled, err := db.CancelAccountDeletion(uid)
	if err != nil {
		t.Fatalf("CancelAccountDeletion: %v", err)
	}
	if !cancelled {
		t.Error("cancel reported nothing to cancel")
	}
	due, err = db.DueAccountDeletions(10)
	if err != nil {
		t.Fatalf("DueAccountDeletions after cancel: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("cancelled account is still due for deletion: %+v", due)
	}

	// Cancelling twice is not an error the interface should invent, but it must
	// report honestly that there was nothing left to do.
	if cancelled, err := db.CancelAccountDeletion(uid); err != nil || cancelled {
		t.Errorf("second cancel returned (%v, %v), want (false, nil)", cancelled, err)
	}
}

// Deleting stored page text must keep the record that the page was crawled —
// that is the difference between this and deleting the crawl history, and the
// interface offers both separately.
func TestPurgingPageContentKeepsTheCrawlRecord(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "content@example.com")
	mustAgedNode(t, db, testURLA, uid, 1)
	if _, err := db.Conn.Exec(
		`UPDATE nodes SET content_hash = 'deadbeef' WHERE url = $1`, testURLA); err != nil {
		t.Fatalf("set hash: %v", err)
	}

	n, err := db.PurgeStoredContent(uid)
	if err != nil {
		t.Fatalf("PurgeStoredContent: %v", err)
	}
	if n != 1 {
		t.Errorf("purged %d rows, want 1", n)
	}
	if got := nodeCount(t, db, uid); got != 1 {
		t.Fatalf("the crawl record itself was deleted: %d remain, want 1", got)
	}

	var content, hash *string
	if err := db.Conn.QueryRow(
		`SELECT content, content_hash FROM nodes WHERE url = $1`, testURLA).Scan(&content, &hash); err != nil {
		t.Fatalf("read node: %v", err)
	}
	if content != nil {
		t.Errorf("page text survived the purge: %q", *content)
	}
	// Leaving the digest behind would make the next crawl compare fresh text
	// against a hash of a copy we no longer hold, decide nothing had changed,
	// and never write content again.
	if hash != nil {
		t.Errorf("content_hash survived the purge: %q", *hash)
	}
}

// Clearing the activity log must not double as a reset button for the counters
// that throttle login attempts and outbound mail.
func TestPurgingActivityLogKeepsLiveThrottlingState(t *testing.T) {
	db := newTestDB(t)
	const ref = "ref-throttle"

	db.LogAuthEvent("login_fail", ref, "ip")
	db.LogAuthEvent("reset_request", ref, "ip")
	db.LogAuthEvent("login_ok", ref, "ip")
	// An old failure is history, not live state, and must go.
	if _, err := db.Conn.Exec(`
		INSERT INTO auth_audit (event, email, ip, created_at)
		VALUES ('login_fail', $1, 'ip', CURRENT_TIMESTAMP - INTERVAL '3 hours')
	`, ref); err != nil {
		t.Fatalf("insert old failure: %v", err)
	}

	if _, err := db.PurgeAuthAudit(ref); err != nil {
		t.Fatalf("PurgeAuthAudit: %v", err)
	}

	if n, err := db.CountRecentAuthEvents("login_fail", ref, 15); err != nil || n != 1 {
		t.Errorf("recent login_fail count is %d (err %v), want 1 — lockout state was cleared", n, err)
	}
	if n, err := db.CountRecentAuthEvents("reset_request", ref, 60); err != nil || n != 1 {
		t.Errorf("recent reset_request count is %d (err %v), want 1 — mail quota was cleared", n, err)
	}
	// Everything that is only history goes: the successful login and the
	// three-hour-old failure.
	var total int
	if err := db.Conn.QueryRow(
		`SELECT COUNT(*) FROM auth_audit WHERE email = $1`, ref).Scan(&total); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if total != 2 {
		t.Errorf("%d audit rows remain, want only the 2 carrying live throttling state", total)
	}
}

// Metadata-only accounts asked us not to hold a copy of other people's pages.
// The digest is not a copy, and keeping it is what lets change detection — and
// therefore recrawl scheduling — keep working without the text.
func TestMetadataOnlyAccountStoresNoPageTextButStillDetectsChange(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "meta@example.com")
	if err := db.SetPrivacySettings(uid, 0, false); err != nil {
		t.Fatalf("SetPrivacySettings: %v", err)
	}

	changed, err := db.SaveNode(testURLA, "A title", "nginx", 200, "completed", "", "the secret page body", "market", uid)
	if err != nil {
		t.Fatalf("SaveNode: %v", err)
	}
	if !changed {
		t.Error("first crawl did not report a content change")
	}

	var content string
	var hash string
	if err := db.Conn.QueryRow(
		`SELECT COALESCE(content, ''), COALESCE(content_hash, '') FROM nodes WHERE url = $1 AND user_id = $2`,
		testURLA, uid).Scan(&content, &hash); err != nil {
		t.Fatalf("read node: %v", err)
	}
	if content != "" {
		t.Errorf("metadata-only account stored page text: %q", content)
	}
	if hash == "" {
		t.Error("no content digest stored — change detection is now impossible")
	}
	// Title and the rest of the metadata are still the point of the crawl.
	var title, category string
	if err := db.Conn.QueryRow(
		`SELECT COALESCE(title, ''), COALESCE(category, '') FROM nodes WHERE url = $1 AND user_id = $2`,
		testURLA, uid).Scan(&title, &category); err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if title != "A title" || category != "market" {
		t.Errorf("metadata was dropped along with the content: title=%q category=%q", title, category)
	}

	// Same text again: unchanged.
	if changed, err := db.SaveNode(testURLA, "A title", "nginx", 200, "completed", "", "the secret page body", "market", uid); err != nil || changed {
		t.Errorf("recrawl of identical content reported changed=%v (err %v), want false", changed, err)
	}
	// Different text: detected, still without storing it.
	changed, err = db.SaveNode(testURLA, "A title", "nginx", 200, "completed", "", "a different body", "market", uid)
	if err != nil {
		t.Fatalf("SaveNode(changed): %v", err)
	}
	if !changed {
		t.Error("changed content was not detected for a metadata-only account")
	}
	if err := db.Conn.QueryRow(
		`SELECT COALESCE(content, '') FROM nodes WHERE url = $1 AND user_id = $2`,
		testURLA, uid).Scan(&content); err != nil {
		t.Fatalf("re-read node: %v", err)
	}
	if content != "" {
		t.Errorf("metadata-only account stored page text on recrawl: %q", content)
	}
}

// The ordinary case must be unaffected: an account that never touched these
// settings keeps getting page content stored.
func TestDefaultAccountStillStoresPageText(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "normal@example.com")

	if _, err := db.SaveNode(testURLA, "A title", "nginx", 200, "completed", "", "body text", "market", uid); err != nil {
		t.Fatalf("SaveNode: %v", err)
	}
	var content string
	if err := db.Conn.QueryRow(
		`SELECT COALESCE(content, '') FROM nodes WHERE url = $1 AND user_id = $2`,
		testURLA, uid).Scan(&content); err != nil {
		t.Fatalf("read node: %v", err)
	}
	if content != "body text" {
		t.Errorf("stored content is %q, want the page body", content)
	}
}

// A retention window the database constraint would refuse has to be rejected
// before it reaches the database: a negative window puts the cutoff in the
// future, which would make the next sweep delete everything the account owns.
func TestRetentionWindowIsRangeChecked(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "range@example.com")

	for _, days := range []int{-1, MaxRetentionDays + 1} {
		if err := db.SetPrivacySettings(uid, days, true); !errors.Is(err, ErrRetentionOutOfRange) {
			t.Errorf("SetPrivacySettings(%d) returned %v, want ErrRetentionOutOfRange", days, err)
		}
	}
	settings, err := db.GetPrivacySettings(uid)
	if err != nil {
		t.Fatalf("GetPrivacySettings: %v", err)
	}
	if settings.RetentionDays != 0 {
		t.Errorf("a rejected value was written anyway: retention_days=%d", settings.RetentionDays)
	}
}
