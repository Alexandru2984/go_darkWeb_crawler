package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/lib/pq"
)

// ErrNodeNotFound is returned when an annotation targets a site the account
// does not have in its own graph.
//
// Not distinguished from "exists but belongs to someone else": answering
// differently would let an account probe which .onion addresses another account
// is tracking, which is the one thing this service exists to keep private.
var ErrNodeNotFound = errors.New("no such site for this account")

// ErrTooManyTags bounds how much an account can attach to one site.
var ErrTooManyTags = errors.New("too many tags on this site")

const (
	// MaxTagLen and MaxTagsPerNode bound a field that is written by a user and
	// read back into a page. Vue escapes on render, so this is about storage and
	// interface sanity rather than injection.
	MaxTagLen      = 40
	MaxTagsPerNode = 25

	// MaxNoteLen is generous enough for real notes and small enough that the
	// column cannot be used as free unbounded storage.
	MaxNoteLen = 4000

	// MinWatchIntervalDays matches the schema constraint. Watching more often
	// than daily would mostly mean hammering someone else's hidden service.
	MinWatchIntervalDays = 1
	MaxWatchIntervalDays = 365
)

// NormalizeTag lowercases and collapses whitespace so "Market", "market " and
// "market" are one tag rather than three that look identical in a list.
func NormalizeTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	return strings.Join(strings.Fields(tag), " ")
}

// nodeIDFor resolves a URL to the calling account's own node row.
//
// Every annotation write goes through this rather than trusting a node id from
// the client. An id is a guessable integer; making the account name the site it
// already has means one account can never attach anything to — or read anything
// about — another account's row.
func nodeIDFor(q interface {
	QueryRow(string, ...any) *sql.Row
}, nodeURL string, userID int) (int, error) {
	canonical, _, err := canonicalCrawlURL(nodeURL)
	if err != nil {
		return 0, err
	}
	var id int
	err = q.QueryRow(`SELECT id FROM nodes WHERE url = $1 AND user_id = $2`, canonical, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNodeNotFound
	}
	return id, err
}

// Annotation is everything one account has attached to one site.
type Annotation struct {
	Tags  []string `json:"tags"`
	Note  string   `json:"note"`
	Watch *Watch   `json:"watch"`
}

// Watch is a site the account is being told about when it changes.
type Watch struct {
	ID            int    `json:"id"`
	URL           string `json:"url"`
	Title         string `json:"title"`
	IntervalDays  int    `json:"interval_days"`
	LastStatus    int    `json:"last_status"`
	LastCheckedAt string `json:"last_checked_at,omitempty"`
	CreatedAt     string `json:"created_at"`
}

// GetAnnotation returns the account's tags, note and watch for one site.
func (db *DB) GetAnnotation(nodeURL string, userID int) (Annotation, error) {
	var a Annotation
	a.Tags = []string{}
	nodeID, err := nodeIDFor(db.Conn, nodeURL, userID)
	if err != nil {
		return a, err
	}

	rows, err := db.Conn.Query(
		`SELECT tag FROM node_tags WHERE user_id = $1 AND node_id = $2 ORDER BY tag`, userID, nodeID)
	if err != nil {
		return a, err
	}
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			rows.Close()
			return a, err
		}
		a.Tags = append(a.Tags, tag)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return a, err
	}

	var note sql.NullString
	if err := db.Conn.QueryRow(
		`SELECT body FROM node_notes WHERE user_id = $1 AND node_id = $2`, userID, nodeID,
	).Scan(&note); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return a, err
	}
	a.Note = note.String

	w, err := db.watchFor(nodeID, userID)
	if err != nil {
		return a, err
	}
	a.Watch = w
	return a, nil
}

func (db *DB) watchFor(nodeID, userID int) (*Watch, error) {
	var w Watch
	var checked sql.NullString
	var status sql.NullInt64
	err := db.Conn.QueryRow(`
		SELECT w.id, n.url, COALESCE(n.title, ''), w.interval_days, w.last_status,
		       TO_CHAR(w.last_checked_at, 'YYYY-MM-DD"T"HH24:MI:SS'),
		       COALESCE(TO_CHAR(w.created_at, 'YYYY-MM-DD"T"HH24:MI:SS'), '')
		  FROM watches w JOIN nodes n ON n.id = w.node_id
		 WHERE w.user_id = $1 AND w.node_id = $2
	`, userID, nodeID).Scan(&w.ID, &w.URL, &w.Title, &w.IntervalDays, &status, &checked, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	w.LastStatus = int(status.Int64)
	w.LastCheckedAt = checked.String
	return &w, nil
}

// AddTag attaches a label to a site. Applying an existing tag again succeeds
// without creating a duplicate.
func (db *DB) AddTag(nodeURL string, userID int, tag string) error {
	tag = NormalizeTag(tag)
	if tag == "" || len(tag) > MaxTagLen {
		return errors.New("invalid tag")
	}
	tx, err := db.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	nodeID, err := nodeIDFor(tx, nodeURL, userID)
	if err != nil {
		return err
	}
	// Counted inside the transaction so two simultaneous additions cannot both
	// see room for the last slot.
	var n int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM node_tags WHERE user_id = $1 AND node_id = $2`, userID, nodeID,
	).Scan(&n); err != nil {
		return err
	}
	if n >= MaxTagsPerNode {
		return ErrTooManyTags
	}
	if _, err := tx.Exec(`
		INSERT INTO node_tags (user_id, node_id, tag) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, node_id, tag) DO NOTHING
	`, userID, nodeID, tag); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveTag detaches a label. Removing one that is not there is not an error.
func (db *DB) RemoveTag(nodeURL string, userID int, tag string) error {
	nodeID, err := nodeIDFor(db.Conn, nodeURL, userID)
	if err != nil {
		return err
	}
	_, err = db.Conn.Exec(
		`DELETE FROM node_tags WHERE user_id = $1 AND node_id = $2 AND tag = $3`,
		userID, nodeID, NormalizeTag(tag))
	return err
}

// SetNote replaces the account's note on a site. An empty body deletes it, so
// clearing the box in the interface does not leave an empty row behind.
func (db *DB) SetNote(nodeURL string, userID int, body string) error {
	body = strings.TrimSpace(body)
	if len(body) > MaxNoteLen {
		return errors.New("note too long")
	}
	nodeID, err := nodeIDFor(db.Conn, nodeURL, userID)
	if err != nil {
		return err
	}
	if body == "" {
		_, err := db.Conn.Exec(
			`DELETE FROM node_notes WHERE user_id = $1 AND node_id = $2`, userID, nodeID)
		return err
	}
	_, err = db.Conn.Exec(`
		INSERT INTO node_notes (user_id, node_id, body) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, node_id) DO UPDATE
		   SET body = EXCLUDED.body, updated_at = CURRENT_TIMESTAMP
	`, userID, nodeID, body)
	return err
}

// TagCount is one of the account's labels with how many sites carry it.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// ListTags returns the account's tags with their usage counts.
func (db *DB) ListTags(userID int) ([]TagCount, error) {
	rows, err := db.Conn.Query(`
		SELECT tag, COUNT(*) FROM node_tags WHERE user_id = $1
		 GROUP BY tag ORDER BY COUNT(*) DESC, tag
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []TagCount{}
	for rows.Next() {
		var t TagCount
		if err := rows.Scan(&t.Tag, &t.Count); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// StartWatch begins watching a site, or updates the interval of an existing
// watch. Setting the recrawl interval on the node itself is part of the same
// change: a watch that never triggers a recrawl would never notice anything.
func (db *DB) StartWatch(nodeURL string, userID, intervalDays int) (*Watch, error) {
	if intervalDays < MinWatchIntervalDays || intervalDays > MaxWatchIntervalDays {
		return nil, errors.New("watch interval out of range")
	}
	tx, err := db.Conn.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	nodeID, err := nodeIDFor(tx, nodeURL, userID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO watches (user_id, node_id, interval_days) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, node_id) DO UPDATE SET interval_days = EXCLUDED.interval_days
	`, userID, nodeID, intervalDays); err != nil {
		return nil, err
	}
	// next_crawl_at is pulled forward only if it was further out, so starting a
	// watch never pushes a sooner scheduled crawl later.
	//
	// $3::int is cast explicitly because the same parameter is also assigned to
	// an integer column above. Interval multiplication takes a double, so
	// without the cast Postgres deduces two different types for one parameter
	// and refuses the statement outright.
	if _, err := tx.Exec(`
		UPDATE nodes SET re_crawl_interval_days = $3,
		                 next_crawl_at = LEAST(next_crawl_at,
		                     COALESCE(last_crawled_at, CURRENT_TIMESTAMP) + ($3::int * INTERVAL '1 day'))
		 WHERE id = $1 AND user_id = $2
	`, nodeID, userID, intervalDays); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return db.watchFor(nodeID, userID)
}

// StopWatch ends a watch and, by cascade, discards its recorded events.
func (db *DB) StopWatch(nodeURL string, userID int) error {
	nodeID, err := nodeIDFor(db.Conn, nodeURL, userID)
	if err != nil {
		return err
	}
	_, err = db.Conn.Exec(`DELETE FROM watches WHERE user_id = $1 AND node_id = $2`, userID, nodeID)
	return err
}

// ListWatches returns everything the account is watching, newest first.
func (db *DB) ListWatches(userID int) ([]Watch, error) {
	rows, err := db.Conn.Query(`
		SELECT w.id, n.url, COALESCE(n.title, ''), w.interval_days, w.last_status,
		       TO_CHAR(w.last_checked_at, 'YYYY-MM-DD"T"HH24:MI:SS'),
		       COALESCE(TO_CHAR(w.created_at, 'YYYY-MM-DD"T"HH24:MI:SS'), '')
		  FROM watches w JOIN nodes n ON n.id = w.node_id
		 WHERE w.user_id = $1 ORDER BY w.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	watches := []Watch{}
	for rows.Next() {
		var w Watch
		var checked sql.NullString
		var status sql.NullInt64
		if err := rows.Scan(&w.ID, &w.URL, &w.Title, &w.IntervalDays, &status, &checked, &w.CreatedAt); err != nil {
			return nil, err
		}
		w.LastStatus = int(status.Int64)
		w.LastCheckedAt = checked.String
		watches = append(watches, w)
	}
	return watches, rows.Err()
}

// AnnotatedSite is one site with whatever the account wrote about it, as
// returned in a personal-data export.
type AnnotatedSite struct {
	URL  string   `json:"url"`
	Tags []string `json:"tags"`
	Note string   `json:"note,omitempty"`
}

// ExportAnnotations streams every site the account has tagged or noted.
//
// This belongs in the export for the same reason the crawl records do, and more
// so: tags and notes are the account's own words, not something a crawler
// produced. An export that returned the machine's output but not the user's
// would be missing the part they would most want back.
func (db *DB) ExportAnnotations(ctx context.Context, userID int, fn func(AnnotatedSite) error) error {
	rows, err := db.Conn.QueryContext(ctx, `
		SELECT n.url,
		       COALESCE(ARRAY_AGG(DISTINCT t.tag) FILTER (WHERE t.tag IS NOT NULL), '{}'),
		       COALESCE(MAX(o.body), '')
		  FROM nodes n
		  LEFT JOIN node_tags  t ON t.node_id = n.id AND t.user_id = $1
		  LEFT JOIN node_notes o ON o.node_id = n.id AND o.user_id = $1
		 WHERE n.user_id = $1 AND (t.tag IS NOT NULL OR o.body IS NOT NULL)
		 GROUP BY n.id, n.url
		 ORDER BY n.url
	`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var s AnnotatedSite
		if err := rows.Scan(&s.URL, pq.Array(&s.Tags), &s.Note); err != nil {
			return err
		}
		if err := fn(s); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Watch event kinds.
const (
	WatchEventChanged     = "content_changed"
	WatchEventUnreachable = "unreachable"
	WatchEventRecovered   = "recovered"
)

// WatchEvent is one entry in the account's change feed.
type WatchEvent struct {
	ID         int    `json:"id"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	StatusCode int    `json:"status_code"`
	DetectedAt string `json:"detected_at"`
	Seen       bool   `json:"seen"`
}

// RecordWatchObservation folds one crawl result into any watch on that site.
//
// Called for both successful and failed crawls, because "this site stopped
// answering" is as much a change as "this page says something different" — and
// for a hidden service it is often the more interesting one.
//
// The comparison is against the watch's own last_hash, never against the node's
// digest directly. The node's digest has already been advanced by the crawl that
// is reporting here, so reading it would mean every observation looked
// unchanged. Advancing the watermark only after the event is written also makes
// this safe to lose: a failure here leaves the watch on its old baseline and the
// next crawl notices the same difference again.
func (db *DB) RecordWatchObservation(nodeURL string, userID int, reachable bool, statusCode int) error {
	canonical, _, err := canonicalCrawlURL(nodeURL)
	if err != nil {
		return err
	}
	tx, err := db.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var watchID int
	var lastHash sql.NullString
	var lastReachable sql.NullBool
	var currentHash sql.NullString
	err = tx.QueryRow(`
		SELECT w.id, w.last_hash, w.last_reachable, n.content_hash
		  FROM watches w JOIN nodes n ON n.id = w.node_id
		 WHERE w.user_id = $1 AND n.url = $2
		 FOR UPDATE OF w
	`, userID, canonical).Scan(&watchID, &lastHash, &lastReachable, &currentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // not watched; nothing to record
	}
	if err != nil {
		return err
	}

	// A server error is "not answering" as much as a refused connection is: for
	// the person watching, a 503 and a dead circuit mean the same thing.
	nowReachable := reachable && (statusCode == 0 || statusCode < 400)

	// A watch that has never been observed is establishing its baseline. There
	// is no previous state to have changed from, and reporting one would greet
	// every new watch with a change it did not ask about.
	baseline := !lastReachable.Valid

	// One observation can be worth more than one event, and reachability does
	// not preempt content. A site that was rewritten while it was down comes
	// back as both "it is answering again" and "it says something different" —
	// reporting only the first, and then advancing the digest, would swallow the
	// change permanently, which is the one thing a watch exists to prevent.
	var kinds []string
	if !baseline {
		switch {
		case lastReachable.Bool && !nowReachable:
			kinds = append(kinds, WatchEventUnreachable)
		case !lastReachable.Bool && nowReachable:
			kinds = append(kinds, WatchEventRecovered)
		}
		// Only compared when the crawl actually fetched something: a failed
		// crawl stored no content, so there is nothing to have changed.
		if nowReachable && lastHash.Valid && currentHash.Valid && lastHash.String != currentHash.String {
			kinds = append(kinds, WatchEventChanged)
		}
	}
	// Only transitions produce reachability events, so a site that has been down
	// for a week yields one entry rather than one per crawl.

	for _, kind := range kinds {
		if _, err := tx.Exec(`
			INSERT INTO watch_events (watch_id, user_id, kind, status_code)
			VALUES ($1, $2, $3, $4)
		`, watchID, userID, kind, statusCode); err != nil {
			return err
		}
	}

	// A failed crawl stored no new content, so its digest is left alone: the
	// watch keeps comparing against the last text it actually saw, and a site
	// that changes while unreachable is still reported once it answers again.
	if nowReachable {
		if _, err := tx.Exec(`UPDATE watches SET last_hash = $2 WHERE id = $1`,
			watchID, currentHash); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		UPDATE watches SET last_status = $2, last_reachable = $3, last_checked_at = CURRENT_TIMESTAMP
		 WHERE id = $1
	`, watchID, statusCode, nowReachable); err != nil {
		return err
	}
	return tx.Commit()
}

// ListWatchEvents returns the account's change feed, newest first.
func (db *DB) ListWatchEvents(userID, limit int) ([]WatchEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Conn.Query(`
		SELECT e.id, n.url, COALESCE(n.title, ''), e.kind, COALESCE(e.status_code, 0),
		       COALESCE(TO_CHAR(e.detected_at, 'YYYY-MM-DD"T"HH24:MI:SS'), ''),
		       e.seen_at IS NOT NULL
		  FROM watch_events e
		  JOIN watches w ON w.id = e.watch_id
		  JOIN nodes n   ON n.id = w.node_id
		 WHERE e.user_id = $1
		 ORDER BY e.detected_at DESC, e.id DESC
		 LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []WatchEvent{}
	for rows.Next() {
		var e WatchEvent
		if err := rows.Scan(&e.ID, &e.URL, &e.Title, &e.Kind, &e.StatusCode, &e.DetectedAt, &e.Seen); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// CountUnseenWatchEvents backs the unread badge.
func (db *DB) CountUnseenWatchEvents(userID int) (int, error) {
	var n int
	err := db.Conn.QueryRow(
		`SELECT COUNT(*) FROM watch_events WHERE user_id = $1 AND seen_at IS NULL`, userID).Scan(&n)
	return n, err
}

// MarkWatchEventsSeen clears the account's unread feed. The user_id condition is
// an authorization boundary, not a filter: without it this would mark another
// account's events as read.
func (db *DB) MarkWatchEventsSeen(userID int) (int64, error) {
	res, err := db.Conn.Exec(
		`UPDATE watch_events SET seen_at = $2 WHERE user_id = $1 AND seen_at IS NULL`,
		userID, time.Now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
