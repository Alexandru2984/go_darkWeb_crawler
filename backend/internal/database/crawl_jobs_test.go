package database

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSchedulePreservesInstantsWithNonUTCDatabaseSessions(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "timezone@example.com")
	if err := db.EnqueueURL(testURLA, 0, uid, 10); err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	query := dsn.Query()
	query.Set("TimeZone", "Europe/Bucharest")
	dsn.RawQuery = query.Encode()
	conn, err := sql.Open("postgres", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	local := &DB{Conn: conn}
	target := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	if _, allowed, err := local.ScheduleCrawl(t.Context(), uid, testURLA, target, 10); err != nil || !allowed {
		t.Fatal("non-UTC schedule failed", err)
	}
	jobs, err := local.ListCrawlJobs(t.Context(), uid, "pending", 10, 0)
	if err != nil || len(jobs) != 1 || jobs[0].ScheduledAt != target.UTC().Format(time.RFC3339) {
		t.Fatal("database timezone shifted the scheduled instant", err)
	}
}

func TestConcurrentImportsSerializeFinalQuotaSlot(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "batch-race@example.com")
	start := make(chan struct{})
	results := make(chan CrawlBatchResult, 2)
	failures := make(chan error, 2)
	for _, target := range []string{testURLA, testURLB} {
		go func() {
			<-start
			result, err := db.CrawlBatch(t.Context(), uid, []string{target}, 1, false)
			results <- result
			failures <- err
		}()
	}
	close(start)
	total := 0
	limited := 0
	for range 2 {
		result, err := <-results, <-failures
		if err != nil {
			t.Fatal(err)
		}
		total += result.Added
		if result.QuotaHit {
			limited++
		}
	}
	if total != 1 || limited != 1 {
		t.Fatal("concurrent imports exceeded final quota slot")
	}
}

func TestSchedulingMetricsExcludePausedFutureAndRefusedWork(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "metrics-jobs@example.com")
	for _, target := range []string{testURLA, testURLB, testURLC} {
		if err := db.EnqueueURL(target, 0, uid, 10); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Conn.Exec(`UPDATE nodes SET next_crawl_at=CURRENT_TIMESTAMP+INTERVAL '1 day' WHERE user_id=$1 AND url=$2`, uid, testURLB); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkRobotsBlocked(testURLC, uid); err != nil {
		t.Fatal(err)
	}
	ready, failed, err := db.GlobalCrawlSchedulingCounts(t.Context())
	if err != nil || ready != 1 || failed != 0 {
		t.Fatal("future/refused work counted as runnable", ready, failed, err)
	}
	if err := db.SetCrawlPaused(t.Context(), uid, true); err != nil {
		t.Fatal(err)
	}
	ready, failed, err = db.GlobalCrawlSchedulingCounts(t.Context())
	if err != nil || ready != 0 || failed != 0 {
		t.Fatal("paused work counted as runnable")
	}
	if _, err := db.Conn.Exec(`UPDATE nodes SET processing_status='crawling' WHERE user_id=$1 AND url=$2`, uid, testURLA); err != nil {
		t.Fatal(err)
	}
	ready, _, err = db.GlobalCrawlSchedulingCounts(t.Context())
	if err != nil || ready != 1 {
		t.Fatal("inflight work hidden by pause")
	}
}

func TestCrawlPauseAndCancelledJobsRespectClaims(t *testing.T) {
	db := newTestDB(t)
	a := mustUser(t, db, "jobs-a@example.com")
	b := mustUser(t, db, "jobs-b@example.com")
	for _, uid := range []int{a, b} {
		if err := db.EnqueueURL(testURLA, 0, uid, 10); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetCrawlPaused(t.Context(), a, true); err != nil {
		t.Fatal(err)
	}
	url, _, uid, err := db.GetNextPendingNode()
	if err != nil || uid != b || url == "" {
		t.Fatal("paused owner claimed or other owner starved", err)
	}
	if err := db.CancelCrawl(t.Context(), a, testURLA); err != nil {
		t.Fatal(err)
	}
	if err := db.SetCrawlPaused(t.Context(), a, false); err != nil {
		t.Fatal(err)
	}
	if url, _, _, err := db.GetNextPendingNode(); err != nil || url != "" {
		t.Fatal("cancelled job claimed", err)
	}
	if found, allowed, err := db.ScheduleCrawl(t.Context(), a, testURLA, time.Time{}, 10); err != nil || !found || !allowed {
		t.Fatal("cancelled job could not resume", err)
	}
	if url, _, uid, err := db.GetNextPendingNode(); err != nil || url == "" || uid != a {
		t.Fatal("resumed job not claimed", err)
	}
	if err := db.CancelCrawl(t.Context(), a, testURLA); !errors.Is(err, ErrCrawlBusy) {
		t.Fatal("active job cancellation accepted")
	}
	if err := db.CancelCrawl(t.Context(), a, testURLB); !errors.Is(err, ErrNodeNotFound) {
		t.Fatal("unknown job visible")
	}
}
func TestClaimSkipsAnOwnerWhosePauseIsCommitting(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "pause-lock@example.com")
	if err := db.EnqueueURL(testURLA, 0, uid, 10); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE users SET crawl_paused=true WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		url, _, _, err := db.GetNextPendingNode()
		if err == nil && url != "" {
			err = errors.New("worker crossed uncommitted pause")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker waited on paused owner instead of skipping it")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if url, _, _, err := db.GetNextPendingNode(); err != nil || url != "" {
		t.Fatal("claim crossed committed pause")
	}
}
func TestCancelAndClaimRaceNeverResurrectsPendingWork(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "cancel-race@example.com")
	for i := range 20 {
		url := fmt.Sprintf("%srace-%d", testURLA, i)
		if err := db.EnqueueURL(url, 0, uid, 100); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		cancelled := make(chan error, 1)
		claimed := make(chan string, 1)
		claimErr := make(chan error, 1)
		go func() { <-start; cancelled <- db.CancelCrawl(t.Context(), uid, url) }()
		go func() { <-start; u, _, _, err := db.GetNextPendingNode(); claimed <- u; claimErr <- err }()
		close(start)
		cancelErr, claimedURL, err := <-cancelled, <-claimed, <-claimErr
		if err != nil {
			t.Fatal(err)
		}
		var status string
		if err := db.Conn.QueryRow(`SELECT processing_status FROM nodes WHERE user_id=$1 AND url=$2`, uid, url).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if cancelErr == nil {
			if status != "cancelled" || claimedURL != "" {
				t.Fatal("cancelled job was claimed")
			}
		} else if !errors.Is(cancelErr, ErrCrawlBusy) || status != "crawling" || claimedURL != url {
			t.Fatal("claim/cancel lost its atomic boundary", cancelErr)
		}
	}
}
func TestManualRetryQuotaSchedulingAndFailurePrivacy(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "retry@example.com")
	researchNode(t, db, uid, testURLA, "Saved", "wiki", "2026-11-10 00:00:00")
	if err := db.EnqueueURL(testURLB, 0, uid, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ScheduleCrawl(t.Context(), uid, testURLA, time.Time{}, 1); !errors.Is(err, ErrQueueQuotaExceeded) {
		t.Fatal("retry bypassed queue quota", err)
	}
	if err := db.CancelCrawl(t.Context(), uid, testURLB); err != nil {
		t.Fatal(err)
	}
	scheduled := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	if found, allowed, err := db.ScheduleCrawl(t.Context(), uid, testURLA, scheduled, 1); err != nil || !found || !allowed {
		t.Fatal("schedule failed", err)
	}
	list, err := db.ListCrawlJobs(t.Context(), uid, "pending", 20, 0)
	if err != nil || len(list) != 1 || list[0].ScheduledAt != scheduled.UTC().Format(time.RFC3339) {
		t.Fatal("scheduled instant lost", err)
	}
	if url, _, _, err := db.GetNextPendingNode(); err != nil || url != "" {
		t.Fatal("future job claimed")
	}
	for _, bad := range []time.Time{time.Now().Add(-2 * time.Minute), time.Now().Add(31 * 24 * time.Hour)} {
		if _, _, err := db.ScheduleCrawl(t.Context(), uid, testURLA, bad, 1); !errors.Is(err, ErrCrawlSchedule) {
			t.Fatal("invalid schedule accepted")
		}
	}
	if err := db.FailNodeWithReason(testURLA, uid, "storage"); err != nil {
		t.Fatal(err)
	}
	list, _ = db.ListCrawlJobs(t.Context(), uid, "", 20, 0)
	var found bool
	for _, job := range list {
		if job.URL == testURLA {
			found = true
			if job.FailureCode != "storage" || job.RetryCount != 1 {
				t.Fatal("failure reason missing")
			}
		}
	}
	if !found {
		t.Fatal("job missing")
	}
	if _, err := db.SaveNode(testURLA, "Saved", "", 200, "completed", "{}", "alpha research text", "wiki", uid); err != nil {
		t.Fatal(err)
	}
	list, _ = db.ListCrawlJobs(t.Context(), uid, "completed", 20, 0)
	if len(list) != 1 || list[0].FailureCode != "" || list[0].RetryCount != 0 {
		t.Fatal("success did not reset failed attempts")
	}
	if err := db.CancelCrawl(t.Context(), uid, testURLA); err != nil {
		t.Fatal(err)
	}
	if err := db.FailNodeWithRetry(testURLA, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveNode(testURLA, "late result", "", 200, "completed", "{}", "late", "wiki", uid); err != nil {
		t.Fatal(err)
	}
	list, _ = db.ListCrawlJobs(t.Context(), uid, "cancelled", 20, 0)
	if len(list) != 2 {
		t.Fatal("late callback resurrected cancelled work")
	}
}
func TestRobotsRefusalIsNotAutomaticallyRevived(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "robots@example.com")
	if err := db.EnqueueURL(testURLA, 0, uid, 10); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkRobotsBlocked(testURLA, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn.Exec(`UPDATE nodes SET next_crawl_at=CURRENT_TIMESTAMP-INTERVAL '80 days' WHERE user_id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if n, err := db.ReviveFailedNodes(7*24*time.Hour, 100); err != nil || n != 0 {
		t.Fatal("robots refusal revived", err)
	}
}
func TestBatchPreviewDeduplicationCapacityAndAtomicFailure(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "batch@example.com")
	if err := db.AddBlacklist(strings.Split(strings.TrimPrefix(testURLC, "http://"), "/")[0]); err != nil {
		t.Fatal(err)
	}
	raw := []string{testURLA, testURLA + "#fragment", "https://example.com/", testURLC, testURLB, testURLA + "overflow"}
	preview, err := db.CrawlBatch(t.Context(), uid, raw, 2, true)
	if err != nil || preview.Added != 2 || preview.Skipped != 4 || !preview.QuotaHit {
		t.Fatal("preview result", preview, err)
	}
	if n, _ := db.CountPendingNodes(uid); n != 0 {
		t.Fatal("preview wrote data")
	}
	applied, err := db.CrawlBatch(t.Context(), uid, raw, 2, false)
	if err != nil || applied.Added != 2 || applied.Items[1].Result != "duplicate" || applied.Items[2].URL != "" || applied.Items[3].Result != "blocked" {
		t.Fatal("batch result", applied, err)
	}
	if err := db.EnqueueURL(testURLA, 0, uid, 2); err != nil {
		t.Fatal("idempotent submission failed at capacity")
	}
	replay, err := db.CrawlBatch(t.Context(), uid, []string{testURLA}, 2, false)
	if err != nil || replay.Added != 0 || replay.Items[0].Result != "already_exists" {
		t.Fatal("duplicate counted as added")
	}
	other := mustUser(t, db, "batch-atomic@example.com")
	_, err = db.Conn.Exec(`CREATE FUNCTION fixture_reject_job() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.url LIKE '%abort' THEN RAISE EXCEPTION 'fixture rejection'; END IF; RETURN NEW; END $$; CREATE TRIGGER fixture_reject_job BEFORE INSERT ON nodes FOR EACH ROW EXECUTE FUNCTION fixture_reject_job()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Conn.Exec(`DROP TRIGGER IF EXISTS fixture_reject_job ON nodes; DROP FUNCTION IF EXISTS fixture_reject_job()`)
	})
	if _, err := db.CrawlBatch(t.Context(), other, []string{testURLA + "okay", testURLA + "abort"}, 10, false); err == nil {
		t.Fatal("forced insert error ignored")
	}
	if n, _ := db.CountPendingNodes(other); n != 0 {
		t.Fatal("failed batch partially committed")
	}
}

func TestCancellationSurvivesBlacklistAndPolicyStillApplies(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "cancel-policy@example.com")
	if err := db.EnqueueURL(testURLA, 0, uid, 10); err != nil {
		t.Fatal(err)
	}
	if err := db.CancelCrawl(t.Context(), uid, testURLA); err != nil {
		t.Fatal(err)
	}
	host := strings.Split(strings.TrimPrefix(testURLA, "http://"), "/")[0]
	if err := db.AddBlacklist(host); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.ListCrawlJobs(t.Context(), uid, "cancelled", 10, 0)
	if err != nil || len(jobs) != 1 {
		t.Fatal("blacklist erased cancellation intent")
	}
	if _, _, err := db.ScheduleCrawl(t.Context(), uid, testURLA, time.Time{}, 10); !errors.Is(err, ErrBlacklisted) {
		t.Fatal("cancelled status bypassed global blacklist", err)
	}
}
