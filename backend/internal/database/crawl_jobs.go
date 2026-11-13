package database

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrCrawlBusy = errors.New("this crawl is running or blocked; its schedule cannot be changed")
var ErrCrawlSchedule = errors.New("choose a scheduled time between now and 30 days ahead")

type CrawlPreferences struct {
	Paused bool `json:"paused"`
}
type CrawlJob struct {
	ID          int    `json:"id"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Depth       int    `json:"depth"`
	RetryCount  int    `json:"retry_count"`
	ScheduledAt string `json:"scheduled_at"`
	StartedAt   string `json:"started_at"`
	FailureCode string `json:"failure_code"`
}

func (db *DB) GetCrawlPreferences(ctx context.Context, uid int) (CrawlPreferences, error) {
	var prefs CrawlPreferences
	err := db.Conn.QueryRowContext(ctx, `SELECT crawl_paused FROM users WHERE id=$1`, uid).Scan(&prefs.Paused)
	return prefs, err
}
func (db *DB) SetCrawlPaused(ctx context.Context, uid int, paused bool) error {
	// Claims hold a SHARE lock on this row before locking a node. The UPDATE
	// waits for those short transactions; no new claim can pass after commit.
	result, err := db.Conn.ExecContext(ctx, `UPDATE users SET crawl_paused=$2 WHERE id=$1`, uid, paused)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return ErrNodeNotFound
	}
	return err
}
func (db *DB) CancelCrawl(ctx context.Context, uid int, raw string) error {
	url, _, err := canonicalCrawlURL(raw)
	if err != nil {
		return err
	}
	result, err := db.Conn.ExecContext(ctx, `UPDATE nodes SET processing_status='cancelled',crawl_started_at=NULL WHERE user_id=$1 AND url=$2 AND processing_status IN ('pending','completed','failed','cancelled')`, uid, url)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var status string
	err = db.Conn.QueryRowContext(ctx, `SELECT processing_status FROM nodes WHERE user_id=$1 AND url=$2`, uid, url).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNodeNotFound
	}
	if err != nil {
		return err
	}
	return ErrCrawlBusy
}
func (db *DB) ScheduleCrawl(ctx context.Context, uid int, raw string, when time.Time, maxPending int) (bool, bool, error) {
	now := time.Now()
	if !when.IsZero() && (when.Before(now.Add(-time.Minute)) || when.After(now.Add(30*24*time.Hour))) {
		return false, false, ErrCrawlSchedule
	}
	if when.IsZero() || when.Before(now) {
		when = now
	}
	url, host, err := canonicalCrawlURL(raw)
	if err != nil {
		return false, false, err
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	if err := lockOnionDomain(tx, host); err != nil {
		return false, false, err
	}
	if err := lockUserQuota(tx, uid); err != nil {
		return false, false, err
	}
	var status string
	err = tx.QueryRowContext(ctx, `SELECT processing_status FROM nodes WHERE user_id=$1 AND url=$2 FOR UPDATE`, uid, url).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if status == "crawling" || status == "blocked" {
		return true, false, nil
	}
	blocked, err := domainBlacklistedTx(tx, host)
	if err != nil {
		return true, false, err
	}
	if blocked {
		return true, false, ErrBlacklisted
	}
	if status != "pending" && maxPending > 0 {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE user_id=$1 AND processing_status='pending'`, uid).Scan(&count); err != nil {
			return true, false, err
		}
		if count >= maxPending {
			return true, false, ErrQueueQuotaExceeded
		}
	}
	// Match the historical TIMESTAMP scheduler by converting the supplied
	// instant into the database session wall time. Output is always explicit UTC.
	_, err = tx.ExecContext(ctx, `UPDATE nodes SET processing_status='pending',next_crawl_at=$3::timestamptz::timestamp,crawl_started_at=NULL,retry_count=0,last_failure_code='' WHERE user_id=$1 AND url=$2`, uid, url, when.UTC())
	if err != nil {
		return true, false, err
	}
	return true, true, tx.Commit()
}
func (db *DB) ListCrawlJobs(ctx context.Context, uid int, status string, limit, offset int) ([]CrawlJob, error) {
	result := make([]CrawlJob, 0)
	err := db.streamCrawlJobs(ctx, uid, status, limit, offset, func(job CrawlJob) error { result = append(result, job); return nil })
	return result, err
}
func (db *DB) ExportCrawlJobs(ctx context.Context, uid int, fn func(CrawlJob) error) error {
	return db.streamCrawlJobs(ctx, uid, "", 0, 0, fn)
}
func (db *DB) streamCrawlJobs(ctx context.Context, uid int, status string, limit, offset int, fn func(CrawlJob) error) error {
	if err := ValidateSearchCriteria("", "", status); err != nil {
		return err
	}
	query := `SELECT id,url,COALESCE(title,''),processing_status,COALESCE(depth,0),COALESCE(retry_count,0),
 COALESCE(to_char(next_crawl_at AT TIME ZONE current_setting('TimeZone') AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),COALESCE(to_char(crawl_started_at AT TIME ZONE current_setting('TimeZone') AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),last_failure_code
 FROM nodes WHERE user_id=$1 AND ($2='' OR processing_status=$2) ORDER BY next_crawl_at ASC NULLS LAST,id ASC`
	args := []any{uid, status}
	if limit > 0 {
		query += " LIMIT $3 OFFSET $4"
		args = append(args, limit, offset)
	}
	rows, err := db.Conn.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var job CrawlJob
		if err := rows.Scan(&job.ID, &job.URL, &job.Title, &job.Status, &job.Depth, &job.RetryCount, &job.ScheduledAt, &job.StartedAt, &job.FailureCode); err != nil {
			return err
		}
		if err := fn(job); err != nil {
			return err
		}
	}
	return rows.Err()
}
func (db *DB) DueCrawlCount(ctx context.Context, uid int) (int, error) {
	var n int
	err := db.Conn.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE user_id=$1 AND processing_status IN ('pending','completed') AND next_crawl_at<=CURRENT_TIMESTAMP`, uid).Scan(&n)
	return n, err
}

// GlobalCrawlSchedulingCounts exposes only aggregate operational counts.
// Active requests remain relevant after pause; future and paused claims do not.
func (db *DB) GlobalCrawlSchedulingCounts(ctx context.Context) (ready, failed int, err error) {
	err = db.Conn.QueryRowContext(ctx, `SELECT
 count(*) FILTER(WHERE n.processing_status='crawling' OR (NOT u.crawl_paused AND n.processing_status IN ('pending','completed') AND n.next_crawl_at<=CURRENT_TIMESTAMP)),
 count(*) FILTER(WHERE NOT u.crawl_paused AND n.processing_status='failed' AND n.last_failure_code NOT IN ('robots','invalid_url'))
 FROM nodes n JOIN users u ON u.id=n.user_id`).Scan(&ready, &failed)
	return
}
