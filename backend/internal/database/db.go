package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"onion-spider/internal/onion"

	"github.com/golang-migrate/migrate/v4"
	pgdriver "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/lib/pq"
)

// migrationsFS embeds the SQL migration files so the binary is self-contained
// and identical across environments. Files live in ./migrations/ next to this
// source file; Go embed paths are relative to the embedding file.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrBlacklisted is returned by EnqueueURL when the domain is on the blacklist.
var ErrBlacklisted = errors.New("domain blocked")

// ErrInvalidOnionURL is returned when an internal caller attempts to put a
// non-canonical or cryptographically invalid onion address into the queue.
var ErrInvalidOnionURL = errors.New("invalid onion URL")

// ErrEmailInUse lets the API distinguish an expected registration conflict
// from a database outage without exposing constraint or account details.
var ErrEmailInUse = errors.New("email already in use")

// ErrQueueQuotaExceeded is returned by EnqueueURL when the account already has
// its full allowance of URLs waiting to be crawled. It is a back-pressure
// signal, not a failure: the caller should tell the user to wait for the queue
// to drain rather than retry.
var ErrQueueQuotaExceeded = errors.New("crawl queue quota exceeded")

type DB struct {
	Conn *sql.DB
}

// InitDB initializes the PostgreSQL connection and runs schema migrations
func InitDB(dsn string) (*DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("error opening database: %w", err)
	}

	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("error connecting to database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	slog.Info("postgres_connected")

	if err = runMigrations(db); err != nil {
		return nil, fmt.Errorf("error running database migrations: %w", err)
	}

	// Nodes left in 'crawling' from a previous crash will never be retried.
	// Reset them to 'pending' on every startup.
	if _, err = db.Exec(`UPDATE nodes SET processing_status = 'pending' WHERE processing_status = 'crawling'`); err != nil {
		slog.Warn("reset_crawling_nodes_failed", "err", err)
	}

	return &DB{Conn: db}, nil
}

// runMigrations applies all pending SQL migrations from the embedded FS using
// golang-migrate. Migrations live in ./migrations and are versioned with
// numeric prefixes (000001_init.up.sql / .down.sql). The migrator records
// state in a schema_migrations table inside the same database, so this is
// safe to run unconditionally on every startup — pending migrations are
// applied, others are no-ops.
func runMigrations(db *sql.DB) error {
	driver, err := pgdriver.WithInstance(db, &pgdriver.Config{})
	if err != nil {
		return fmt.Errorf("init migrate driver: %w", err)
	}

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("load migration sources: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	if err := migrateOpaqueTokens(db); err != nil {
		return fmt.Errorf("migrate opaque credentials: %w", err)
	}

	v, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("read migration version: %w", err)
	}
	slog.Info("migrate_complete", "version", v, "dirty", dirty)
	return nil
}

const opaqueTokenMigration = "hash-opaque-tokens-v1"

// migrateOpaqueTokens converts verification and password-reset credentials
// created by older releases from plaintext to SHA-256 digests. A transaction
// and advisory lock make the data migration crash-safe and serialize startup
// if multiple application instances are launched at once.
func migrateOpaqueTokens(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const migrationLock int64 = 0x4f535049444552 // "OSPIDER"
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
		return err
	}

	var applied bool
	if err := tx.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM security_data_migrations WHERE name = $1
		)
	`, opaqueTokenMigration).Scan(&applied); err != nil {
		return err
	}
	if applied {
		return tx.Commit()
	}

	type legacyTokens struct {
		id           int
		verification sql.NullString
		reset        sql.NullString
	}
	rows, err := tx.Query(`
		SELECT id, verification_token, reset_token
		FROM users
		WHERE verification_token IS NOT NULL OR reset_token IS NOT NULL
		FOR UPDATE
	`)
	if err != nil {
		return err
	}
	var users []legacyTokens
	for rows.Next() {
		var u legacyTokens
		if err := rows.Scan(&u.id, &u.verification, &u.reset); err != nil {
			rows.Close()
			return err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, u := range users {
		var verification, reset any
		if u.verification.Valid {
			verification = opaqueTokenHash(u.verification.String)
		}
		if u.reset.Valid {
			reset = opaqueTokenHash(u.reset.String)
		}
		if _, err := tx.Exec(`
			UPDATE users SET verification_token = $2, reset_token = $3 WHERE id = $1
		`, u.id, verification, reset); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`INSERT INTO security_data_migrations (name) VALUES ($1)`, opaqueTokenMigration); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	slog.Info("opaque_token_migration_complete", "accounts", len(users))
	return nil
}

// Node represents an onion site stored in the DB
type Node struct {
	ID               int    `json:"id"`
	URL              string `json:"url"`
	Title            string `json:"title"`
	StatusCode       int    `json:"status_code"`
	ServerHeader     string `json:"server_header"`
	ProcessingStatus string `json:"processing_status"`
	Category         string `json:"category"`
	LastCrawledAt    string `json:"last_crawled_at,omitempty"`
	UserID           int    `json:"user_id"`
}

// NodeDetail includes the full content — used for GET /api/node
type NodeDetail struct {
	Node
	Content      string `json:"content"`
	Metadata     string `json:"metadata"`
	ContentHash  string `json:"content_hash,omitempty"`
	DiscoveredAt string `json:"discovered_at"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// ContentHash computes sha256(title + "|" + content) for change detection
func ContentHash(title, content string) string {
	h := sha256.Sum256([]byte(title + "|" + content))
	return fmt.Sprintf("%x", h)
}

// opaqueTokenHash is the only representation of verification/reset tokens that
// may be persisted. Tokens carry 256 bits of crypto/rand entropy, so SHA-256 is
// sufficient here: a database or backup compromise cannot recover a usable
// single-use credential from its digest.
func opaqueTokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", h)
}

func canonicalCrawlURL(rawURL string) (canonical, hostname string, err error) {
	canonical = onion.NormalizeURL(rawURL)
	if canonical == "" {
		return "", "", ErrInvalidOnionURL
	}
	parsed, parseErr := url.Parse(canonical)
	if parseErr != nil || parsed.Hostname() == "" {
		return "", "", ErrInvalidOnionURL
	}
	return canonical, strings.ToLower(parsed.Hostname()), nil
}

func lockOnionDomain(tx *sql.Tx, hostname string) error {
	_, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, hostname)
	return err
}

func domainBlacklistedTx(tx *sql.Tx, hostname string) (bool, error) {
	var blocked bool
	err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM blacklist WHERE domain = $1)`, hostname).Scan(&blocked)
	return blocked, err
}

// SaveNode saves or updates information about an onion site after crawling.
// Returns (contentChanged bool, error). If the content hash hasn't changed,
// performs a minimal update (without touching content or tsvector).
func (db *DB) SaveNode(nodeURL, title, server string, statusCode int, status string, metadata string, content string, category string, userID int) (bool, error) {
	canonical, _, err := canonicalCrawlURL(nodeURL)
	if err != nil {
		return false, err
	}
	nodeURL = canonical
	if metadata == "" {
		metadata = "{}"
	}
	newHash := ContentHash(title, content)

	// Check if the hash has changed since the last visit
	var oldHash sql.NullString
	_ = db.Conn.QueryRow(`SELECT content_hash FROM nodes WHERE url = $1 AND user_id = $2`, nodeURL, userID).Scan(&oldHash)

	contentChanged := !oldHash.Valid || oldHash.String != newHash

	if contentChanged {
		// New content or first visit: full update
		_, err := db.Conn.Exec(`
		INSERT INTO nodes (url, title, status_code, server_header, processing_status, metadata, content, content_hash, category, last_crawled_at, next_crawl_at, user_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP + (INTERVAL '1 day' * 7), $10) ON CONFLICT (url, user_id) DO UPDATE SET
			title             = EXCLUDED.title,
			status_code       = EXCLUDED.status_code,
			server_header     = EXCLUDED.server_header,
			processing_status = EXCLUDED.processing_status,
			metadata          = EXCLUDED.metadata,
			content           = EXCLUDED.content,
			content_hash      = EXCLUDED.content_hash,
			category          = EXCLUDED.category,
			last_crawled_at   = CURRENT_TIMESTAMP,
			next_crawl_at     = CURRENT_TIMESTAMP + (INTERVAL '1 day' * nodes.re_crawl_interval_days)
		WHERE nodes.processing_status != 'blocked';
		`, nodeURL, title, statusCode, server, status, metadata, content, newHash, category, userID)
		return true, err
	}

	// Unchanged content: minimal update — don't touch content/tsvector/category
	_, err = db.Conn.Exec(`
	UPDATE nodes SET
		status_code       = $3,
		server_header     = $4,
		processing_status = $5,
		last_crawled_at   = CURRENT_TIMESTAMP,
		next_crawl_at     = CURRENT_TIMESTAMP + (INTERVAL '1 day' * re_crawl_interval_days)
	WHERE url = $1 AND user_id = $2 AND processing_status != 'blocked'
	`, nodeURL, userID, statusCode, server, status)
	return false, err
}

// EnqueueURL adds a URL to the crawling queue without overwriting existing data.
// Returns ErrBlacklisted if the domain is on the blacklist.
//
// maxPending caps how much unprocessed work one account may have waiting at
// once; zero or less disables the cap. The count is taken inside the same
// transaction as the insert, and behind the same per-domain advisory lock, so
// concurrent submissions cannot each observe room that only one of them has.
// Only user-submitted URLs pass through here — links discovered mid-crawl are
// inserted by SaveEdge and are bounded by MaxDepth instead, so raising this cap
// does not change how deep an individual crawl goes.
func (db *DB) EnqueueURL(rawURL string, depth int, userID int, maxPending int) error {
	canonical, host, err := canonicalCrawlURL(rawURL)
	if err != nil {
		return err
	}
	tx, err := db.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockOnionDomain(tx, host); err != nil {
		return fmt.Errorf("lock onion domain: %w", err)
	}
	blocked, err := domainBlacklistedTx(tx, host)
	if err != nil {
		return fmt.Errorf("check blacklist: %w", err)
	}
	if blocked {
		return ErrBlacklisted
	}
	if maxPending > 0 {
		var pending int
		if err := tx.QueryRow(
			`SELECT COUNT(*) FROM nodes WHERE user_id = $1 AND processing_status = 'pending'`,
			userID,
		).Scan(&pending); err != nil {
			return fmt.Errorf("count pending: %w", err)
		}
		if pending >= maxPending {
			return ErrQueueQuotaExceeded
		}
	}
	_, err = tx.Exec(
		`INSERT INTO nodes (url, host, processing_status, depth, user_id) VALUES ($1, $2, 'pending', $3, $4) ON CONFLICT (url, user_id) DO NOTHING`,
		canonical, host, depth, userID,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// CountPendingNodes reports how many URLs an account has waiting to be crawled.
func (db *DB) CountPendingNodes(userID int) (int, error) {
	var n int
	err := db.Conn.QueryRow(
		`SELECT COUNT(*) FROM nodes WHERE user_id = $1 AND processing_status = 'pending'`,
		userID,
	).Scan(&n)
	return n, err
}

// SearchNodes performs a Full-Text search on title and content using the GIN index.
// If category is non-empty, also filters by category.
func (db *DB) SearchNodes(searchQuery, category string, userID int, isAdmin bool) ([]Node, error) {
	rows, err := db.Conn.Query(`
		SELECT id, url, COALESCE(title, ''), COALESCE(status_code, 0), COALESCE(server_header, ''),
		       processing_status, COALESCE(category, 'unknown'),
		       to_char(last_crawled_at, 'YYYY-MM-DD HH24:MI:SS')
		FROM nodes
		WHERE search_vector @@ plainto_tsquery('english', $1)
		  AND ($2 = '' OR category = $2)
		  AND (user_id = $3 OR $4)
		ORDER BY ts_rank(search_vector, plainto_tsquery('english', $1)) DESC
		LIMIT 50
	`, searchQuery, category, userID, isAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		var lastCrawled sql.NullString
		if err := rows.Scan(&n.ID, &n.URL, &n.Title, &n.StatusCode, &n.ServerHeader, &n.ProcessingStatus, &n.Category, &lastCrawled); err != nil {
			return nil, err
		}
		if lastCrawled.Valid {
			n.LastCrawledAt = lastCrawled.String
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// SaveEdge creates a link between two sites and adds the target to the queue if new.
// If the URL already exists at a greater depth, updates the depth (more efficient crawl).
// Domains on the blacklist are skipped (not added to the queue).
func (db *DB) SaveEdge(source, target string, targetDepth int, userID int) error {
	canonicalSource, _, err := canonicalCrawlURL(source)
	if err != nil {
		return fmt.Errorf("invalid edge source: %w", err)
	}
	canonicalTarget, targetHost, err := canonicalCrawlURL(target)
	if err != nil {
		return fmt.Errorf("invalid edge target: %w", err)
	}
	source, target = canonicalSource, canonicalTarget

	tx, err := db.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockOnionDomain(tx, targetHost); err != nil {
		return fmt.Errorf("lock target onion domain: %w", err)
	}

	// Check the blacklist before adding the target node to the queue
	blocked, err := domainBlacklistedTx(tx, targetHost)
	if err != nil {
		return fmt.Errorf("check target blacklist: %w", err)
	}
	if !blocked {
		if _, err := tx.Exec(
			`INSERT INTO nodes (url, host, processing_status, depth, user_id) VALUES ($1, $2, 'pending', $3, $4) ON CONFLICT (url, user_id) DO UPDATE
				   SET depth = EXCLUDED.depth
				   WHERE nodes.depth > EXCLUDED.depth AND nodes.processing_status = 'pending'`,
			target, targetHost, targetDepth, userID,
		); err != nil {
			return fmt.Errorf("enqueue edge target: %w", err)
		}
	}

	_, err = tx.Exec(`
	INSERT INTO edges (source_url, target_url, user_id) VALUES ($1, $2, $3) ON CONFLICT (source_url, target_url, user_id) DO NOTHING
	`, source, target, userID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// GetNextPendingNode atomically claims the next URL to crawl, with fair
// scheduling across tenants.
//
// Fairness: candidates are ordered first by how many of the owning user's nodes
// are already in-flight ('crawling'), so a worker always prefers a user with
// fewer active crawls. This is work-conserving — a single tenant alone gets
// every worker — but under contention the pool is shared evenly instead of one
// user with a huge backlog starving everyone else.
//
// Within a user, 'pending' beats expired 'completed' (re-crawl), then oldest
// next_crawl_at. The candidate row is locked with FOR UPDATE OF n SKIP LOCKED
// so concurrent workers never collide. The match is on the full (url, user_id)
// key — matching on url alone would flip every tenant's copy of the same URL to
// 'crawling' at once.
func (db *DB) GetNextPendingNode() (string, int, int, error) {
	var nodeURL string
	var depth int
	var userID int
	err := db.Conn.QueryRow(`
		UPDATE nodes
		SET processing_status = 'crawling',
		    crawl_started_at  = CURRENT_TIMESTAMP
		WHERE (url, user_id) = (
			SELECT n.url, n.user_id
			FROM nodes n
			LEFT JOIN (
				SELECT user_id, COUNT(*) AS inflight
				FROM nodes
				WHERE processing_status = 'crawling'
				GROUP BY user_id
			) c ON c.user_id = n.user_id
			WHERE n.processing_status IN ('pending', 'completed')
			  AND n.next_crawl_at <= CURRENT_TIMESTAMP
			ORDER BY
				COALESCE(c.inflight, 0) ASC,
				CASE WHEN n.processing_status = 'pending' THEN 0 ELSE 1 END ASC,
				n.next_crawl_at ASC
			LIMIT 1
			FOR UPDATE OF n SKIP LOCKED
		)
		RETURNING url, depth, user_id
	`).Scan(&nodeURL, &depth, &userID)
	if err == sql.ErrNoRows {
		return "", 0, 0, nil
	}
	return nodeURL, depth, userID, err
}

// ResetStuckCrawling resets nodes that have been in the 'crawling' state longer
// than olderThan (e.g., after a hard crash of a worker). Returns the number
// of recovered nodes.
func (db *DB) ResetStuckCrawling(olderThan time.Duration) (int64, error) {
	res, err := db.Conn.Exec(`
		UPDATE nodes
		SET processing_status = 'pending',
		    crawl_started_at  = NULL
		WHERE processing_status = 'crawling'
		  AND crawl_started_at IS NOT NULL
		  AND crawl_started_at < CURRENT_TIMESTAMP - ($1 || ' seconds')::INTERVAL
	`, fmt.Sprintf("%d", int(olderThan.Seconds())))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// FailNodeWithRetry records a failure and schedules a retry with exponential backoff.
// Formula: min(2^retry * 10min, 48h). 'blocked' nodes are never modified.
func (db *DB) FailNodeWithRetry(nodeURL string, userID int) error {
	_, err := db.Conn.Exec(`
		UPDATE nodes
		SET retry_count       = retry_count + 1,
		    processing_status = CASE WHEN retry_count >= 4 THEN 'failed' ELSE 'pending' END,
		    next_crawl_at     = CURRENT_TIMESTAMP + (
		        LEAST(
		            INTERVAL '1 minute' * (10 * POW(2, retry_count)),
		            INTERVAL '48 hours'
		        )
		    )
		WHERE url = $1 AND user_id = $2 AND processing_status != 'blocked'
	`, nodeURL, userID)
	return err
}

// Stats holds summary statistics about the crawling state
type Stats struct {
	NodesCrawled  int
	PendingNodes  int
	FailedNodes   int
	CrawlingNodes int
	BlockedNodes  int
	TotalEdges    int
}

// GetStats returns complete statistics about the crawling state
func (db *DB) GetStats(userID int, isAdmin bool) (Stats, error) {
	var s Stats
	err := db.Conn.QueryRow(`
		SELECT
			COUNT(*) FILTER (WHERE processing_status = 'completed'),
			COUNT(*) FILTER (WHERE processing_status = 'pending'),
			COUNT(*) FILTER (WHERE processing_status = 'failed'),
			COUNT(*) FILTER (WHERE processing_status = 'crawling'),
			COUNT(*) FILTER (WHERE processing_status = 'blocked')
		FROM nodes
		WHERE (user_id = $1 OR $2)
	`, userID, isAdmin).Scan(&s.NodesCrawled, &s.PendingNodes, &s.FailedNodes, &s.CrawlingNodes, &s.BlockedNodes)
	if err != nil {
		return s, err
	}
	err = db.Conn.QueryRow(`SELECT COUNT(*) FROM edges WHERE (user_id = $1 OR $2)`, userID, isAdmin).Scan(&s.TotalEdges)
	return s, err
}

// GetNodeByURL returns the complete details of a node by URL
func (db *DB) GetNodeByURL(nodeURL string, userID int, isAdmin bool) (*NodeDetail, error) {
	var n NodeDetail
	var lastCrawled, discovered sql.NullString
	var contentHash sql.NullString
	err := db.Conn.QueryRow(`
		SELECT id, url, COALESCE(title,''), COALESCE(status_code,0), COALESCE(server_header,''),
		       processing_status, COALESCE(category,'unknown'),
		       COALESCE(content,''), COALESCE(metadata,'{}'), content_hash,
		       to_char(last_crawled_at,'YYYY-MM-DD HH24:MI:SS'),
		       to_char(discovered_at,'YYYY-MM-DD HH24:MI:SS')
		FROM nodes WHERE url = $1 AND (user_id = $2 OR $3)
	`, nodeURL, userID, isAdmin).Scan(
		&n.ID, &n.URL, &n.Title, &n.StatusCode, &n.ServerHeader,
		&n.ProcessingStatus, &n.Category,
		&n.Content, &n.Metadata, &contentHash,
		&lastCrawled, &discovered,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastCrawled.Valid {
		n.LastCrawledAt = lastCrawled.String
	}
	if discovered.Valid {
		n.DiscoveredAt = discovered.String
	}
	if contentHash.Valid {
		n.ContentHash = contentHash.String
	}
	return &n, nil
}

// RequeueForCrawl resets a node to 'pending' for immediate re-crawl.
// Returns (found bool, canRequeue bool, error).
// canRequeue=false if the node is already in the 'crawling' state.
func (db *DB) RequeueForCrawl(nodeURL string, userID int) (found bool, canRequeue bool, err error) {
	var status string
	err = db.Conn.QueryRow(`SELECT processing_status FROM nodes WHERE url = $1 AND user_id = $2`, nodeURL, userID).Scan(&status)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if status == "crawling" || status == "blocked" {
		return true, false, nil
	}
	_, err = db.Conn.Exec(
		`UPDATE nodes SET processing_status = 'pending', next_crawl_at = CURRENT_TIMESTAMP WHERE url = $1 AND user_id = $2`,
		nodeURL, userID,
	)
	return true, true, err
}

// MarkRobotsBlocked marks a node as disallowed by robots.txt.
// Sets next_crawl_at to 30 days and retry_count=10 to prevent unnecessary retries.
// Does not overwrite nodes explicitly blocked via the blacklist.
func (db *DB) MarkRobotsBlocked(nodeURL string, userID int) error {
	_, err := db.Conn.Exec(`
		UPDATE nodes SET
			processing_status = 'failed',
			retry_count       = 10,
			next_crawl_at     = CURRENT_TIMESTAMP + INTERVAL '30 days'
		WHERE url = $1 AND user_id = $2 AND processing_status != 'blocked'
	`, nodeURL, userID)
	return err
}

func (db *DB) GetNodes(limit, offset int, userID int, isAdmin bool) ([]Node, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Conn.Query(`
		SELECT id, url, COALESCE(title, ''), COALESCE(status_code, 0), COALESCE(server_header, ''),
		       processing_status, COALESCE(category, 'unknown'),
		       to_char(last_crawled_at, 'YYYY-MM-DD HH24:MI:SS')
		FROM nodes WHERE (user_id = $3 OR $4) ORDER BY discovered_at DESC LIMIT $1 OFFSET $2`, limit, offset, userID, isAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		var lastCrawled sql.NullString
		if err := rows.Scan(&n.ID, &n.URL, &n.Title, &n.StatusCode, &n.ServerHeader, &n.ProcessingStatus, &n.Category, &lastCrawled); err != nil {
			return nil, err
		}
		if lastCrawled.Valid {
			n.LastCrawledAt = lastCrawled.String
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func (db *DB) GetEdges(limit, offset int, userID int, isAdmin bool) ([]Edge, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := db.Conn.Query(`SELECT source_url, target_url FROM edges WHERE (user_id = $3 OR $4) LIMIT $1 OFFSET $2`, limit, offset, userID, isAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.Source, &e.Target); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// QueueSummary contains statistics about the crawling queue
type QueueSummary struct {
	StatusCounts map[string]int `json:"status_counts"`
	NextItems    []QueueItem    `json:"next_items"`
}

// QueueItem represents a URL in the queue, with basic metadata
type QueueItem struct {
	URL          string `json:"url"`
	Depth        int    `json:"depth"`
	Status       string `json:"status"`
	DiscoveredAt string `json:"discovered_at"`
}

// GetQueueSummary returns counts by status and the next 10 URLs in the queue
func (db *DB) GetQueueSummary(userID int, isAdmin bool) (*QueueSummary, error) {
	rows, err := db.Conn.Query(`
		SELECT processing_status, COUNT(*) FROM nodes
		WHERE (user_id = $1 OR $2)
		GROUP BY processing_status
	`, userID, isAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	next, err := db.Conn.Query(`
		SELECT url, depth, processing_status, to_char(discovered_at, 'YYYY-MM-DD HH24:MI:SS')
		FROM nodes
		WHERE processing_status = 'pending' AND (user_id = $1 OR $2)
		ORDER BY next_crawl_at ASC
		LIMIT 10
	`, userID, isAdmin)
	if err != nil {
		return nil, err
	}
	defer next.Close()

	var items []QueueItem
	for next.Next() {
		var item QueueItem
		var disc sql.NullString
		if err := next.Scan(&item.URL, &item.Depth, &item.Status, &disc); err != nil {
			return nil, err
		}
		if disc.Valid {
			item.DiscoveredAt = disc.String
		}
		items = append(items, item)
	}
	return &QueueSummary{StatusCounts: counts, NextItems: items}, next.Err()
}

// AddBlacklist adds a domain to the blacklist (sets processing_status='blocked' on all nodes with that domain)
// and prevents future addition of URLs from that domain.
func (db *DB) AddBlacklist(domain string) error {
	domain = onion.NormalizeHostname(domain)
	if domain == "" {
		return ErrInvalidOnionURL
	}
	tx, err := db.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockOnionDomain(tx, domain); err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO blacklist (domain) VALUES ($1) ON CONFLICT (domain) DO NOTHING
	`, domain)
	if err != nil {
		return err
	}
	// Block all existing nodes with this domain (exact match on the host column)
	_, err = tx.Exec(`
		UPDATE nodes SET processing_status = 'blocked'
		WHERE host = $1 AND processing_status != 'blocked'
	`, domain)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// GetBlacklist returns all blocked domains
func (db *DB) GetBlacklist() ([]string, error) {
	rows, err := db.Conn.Query(`SELECT domain FROM blacklist ORDER BY added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var domains []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		domains = append(domains, d)
	}
	return domains, rows.Err()
}

// IsDomainBlacklisted checks whether a domain is blocked
func (db *DB) IsDomainBlacklisted(domain string) (bool, error) {
	domain = onion.NormalizeHostname(domain)
	if domain == "" {
		return false, ErrInvalidOnionURL
	}
	var blocked bool
	err := db.Conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM blacklist WHERE domain = $1)`, domain).Scan(&blocked)
	return blocked, err
}

// ExportNodes returns all fully crawled nodes in discovery order.
// ctx allows cancellation if the client disconnects.
// Uses a cursor to avoid loading the entire table into memory.
func (db *DB) ExportNodes(ctx context.Context, userID int, isAdmin bool, fn func(Node) error) error {
	rows, err := db.Conn.QueryContext(ctx, `
		SELECT id, url, COALESCE(title,''), COALESCE(status_code,0), COALESCE(server_header,''),
		       processing_status, COALESCE(category,'unknown'),
		       to_char(last_crawled_at,'YYYY-MM-DD HH24:MI:SS')
		FROM nodes
		WHERE processing_status = 'completed' AND (user_id = $1 OR $2)
		ORDER BY discovered_at ASC
	`, userID, isAdmin)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n Node
		var lastCrawled sql.NullString
		if err := rows.Scan(&n.ID, &n.URL, &n.Title, &n.StatusCode, &n.ServerHeader,
			&n.ProcessingStatus, &n.Category, &lastCrawled); err != nil {
			return err
		}
		if lastCrawled.Valid {
			n.LastCrawledAt = lastCrawled.String
		}
		if err := fn(n); err != nil {
			return err
		}
	}
	return rows.Err()
}

// DeleteBlacklist removes a domain from the blacklist and puts blocked nodes back in the queue.
// Returns (found bool, error).
func (db *DB) DeleteBlacklist(domain string) (bool, error) {
	domain = onion.NormalizeHostname(domain)
	if domain == "" {
		return false, ErrInvalidOnionURL
	}
	tx, err := db.Conn.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := lockOnionDomain(tx, domain); err != nil {
		return false, err
	}
	res, err := tx.Exec(`DELETE FROM blacklist WHERE domain = $1`, domain)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return false, nil
	}
	_, err = tx.Exec(`
		UPDATE nodes SET processing_status = 'pending', retry_count = 0, next_crawl_at = CURRENT_TIMESTAMP
		WHERE host = $1 AND processing_status = 'blocked'
	`, domain)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// GraphMLEdge represents an edge exported for GraphML — both endpoints are completed nodes.
type GraphMLEdge struct {
	SourceID int
	TargetID int
}

// ExportGraphMLEdges returns edges where both nodes (source and target) are completed.
// Uses JOIN to guarantee consistency of the exported graph.
func (db *DB) ExportGraphMLEdges(ctx context.Context, userID int, isAdmin bool, fn func(GraphMLEdge) error) error {
	rows, err := db.Conn.QueryContext(ctx, `
		SELECT n1.id, n2.id
		FROM edges e
		JOIN nodes n1 ON n1.url = e.source_url AND n1.user_id = e.user_id AND n1.processing_status = 'completed'
		JOIN nodes n2 ON n2.url = e.target_url AND n2.user_id = e.user_id AND n2.processing_status = 'completed'
		WHERE (e.user_id = $1 OR $2)
	`, userID, isAdmin)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ge GraphMLEdge
		if err := rows.Scan(&ge.SourceID, &ge.TargetID); err != nil {
			return err
		}
		if err := fn(ge); err != nil {
			return err
		}
	}
	return rows.Err()
}

type TimelineStat struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// GetTimelineStats returns the number of nodes discovered per day over the last 30 days.
func (db *DB) GetTimelineStats(userID int, isAdmin bool) ([]TimelineStat, error) {
	rows, err := db.Conn.Query(`
		SELECT to_char(discovered_at, 'YYYY-MM-DD') AS date, COUNT(*) AS count
		FROM nodes
		WHERE discovered_at >= CURRENT_DATE - INTERVAL '30 days' AND (user_id = $1 OR $2)
		GROUP BY date
		ORDER BY date ASC
	`, userID, isAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stats []TimelineStat
	for rows.Next() {
		var s TimelineStat
		if err := rows.Scan(&s.Date, &s.Count); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// User model
type User struct {
	ID           int    `json:"id"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	IsVerified   bool   `json:"is_verified"`
	TokenVersion int    `json:"-"`
	CreatedAt    string `json:"created_at"`
}

// NormalizeEmail returns the email in lowercase and without spaces.
// All email operations must go through this.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// CreateUser adds a new user with a verification token valid for 24h.
func (db *DB) CreateUser(email, passwordHash, role, token string) error {
	if role != "user" && role != "admin" {
		return errors.New("invalid user role")
	}
	_, err := db.Conn.Exec(`
		INSERT INTO users (email, password_hash, role, verification_token, verification_expires_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP + INTERVAL '24 hours')
	`, NormalizeEmail(email), passwordHash, role, opaqueTokenHash(token))
	return err
}

// CreateRegisteredUser atomically assigns the bootstrap admin role and creates
// a public-registration account. The advisory transaction lock prevents two
// differently configured application instances from both observing an empty
// admin set and creating separate administrators.
func (db *DB) CreateRegisteredUser(email, passwordHash, token, adminEmail string) (string, error) {
	email = NormalizeEmail(email)
	adminEmail = NormalizeEmail(adminEmail)
	tx, err := db.Conn.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	const bootstrapAdminLock int64 = 0x4f535041444d494e // "OSPADMIN"
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, bootstrapAdminLock); err != nil {
		return "", err
	}

	role := "user"
	if adminEmail != "" && email == adminEmail {
		var hasAdmin bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE role = 'admin')`).Scan(&hasAdmin); err != nil {
			return "", err
		}
		if !hasAdmin {
			role = "admin"
		}
	}

	if _, err := tx.Exec(`
		INSERT INTO users (email, password_hash, role, verification_token, verification_expires_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP + INTERVAL '24 hours')
	`, email, passwordHash, role, opaqueTokenHash(token)); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return "", ErrEmailInUse
		}
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return role, nil
}

// GetUserByEmail fetches a user by email (case-insensitive).
func (db *DB) GetUserByEmail(email string) (*User, error) {
	var u User
	err := db.Conn.QueryRow(`
		SELECT id, email, password_hash, role, is_verified, token_version, to_char(created_at, 'YYYY-MM-DD HH24:MI:SS')
		FROM users WHERE email = $1
	`, NormalizeEmail(email)).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.IsVerified, &u.TokenVersion, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

// SetResetToken stores a password-reset token (valid 1h) for the user with the
// given email, if one exists. Returns (found, error). Callers must NOT reveal
// `found` to the client — that would enable account enumeration.
func (db *DB) SetResetToken(email, token string) (bool, error) {
	res, err := db.Conn.Exec(`
		UPDATE users
		SET reset_token = $2, reset_expires_at = CURRENT_TIMESTAMP + INTERVAL '1 hour'
		WHERE email = $1
	`, NormalizeEmail(email), opaqueTokenHash(token))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ResetPassword consumes a valid, unexpired reset token: sets the new password
// hash, clears the token, and bumps token_version to revoke every existing
// session. Returns an error if the token is invalid/expired/used.
func (db *DB) ResetPassword(token, newHash string) error {
	if len(token) < 16 {
		return errors.New("invalid token")
	}
	res, err := db.Conn.Exec(`
		UPDATE users
		SET password_hash    = $2,
		    reset_token      = NULL,
		    reset_expires_at = NULL,
		    token_version    = token_version + 1
		WHERE reset_token = $1
		  AND reset_expires_at IS NOT NULL
		  AND reset_expires_at > CURRENT_TIMESTAMP
	`, opaqueTokenHash(token), newHash)
	if err != nil {
		return err
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return errors.New("token invalid, expired or already used")
	}
	return nil
}

// BumpTokenVersion increments the user's token_version, invalidating all
// previously-issued JWTs (logout everywhere).
func (db *DB) BumpTokenVersion(userID int) error {
	_, err := db.Conn.Exec(`UPDATE users SET token_version = token_version + 1 WHERE id = $1`, userID)
	return err
}

// VerifyUser sets the user as verified, only if the token has not expired.
func (db *DB) VerifyUser(token string) error {
	if len(token) < 16 {
		return errors.New("invalid token")
	}
	res, err := db.Conn.Exec(`
		UPDATE users
		SET is_verified = TRUE, verification_token = NULL, verification_expires_at = NULL
		WHERE verification_token = $1
		  AND is_verified = FALSE
		  AND (verification_expires_at IS NULL OR verification_expires_at > CURRENT_TIMESTAMP)
	`, opaqueTokenHash(token))
	if err != nil {
		return err
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return errors.New("token invalid, expired or already used")
	}
	return nil
}

// LogAuthEvent inserts privacy-preserving references into auth_audit. Callers
// must HMAC raw email/IP identifiers before invoking this method; the legacy
// column names remain for migration compatibility. The write is best-effort and
// does not block the auth flow if auditing is temporarily unavailable.
func (db *DB) LogAuthEvent(event, emailRef, ipRef string) {
	_, err := db.Conn.Exec(
		`INSERT INTO auth_audit (event, email, ip) VALUES ($1, $2, $3)`,
		event, emailRef, ipRef,
	)
	if err != nil {
		slog.Error("auth_audit_write_failed", "event", event, "err", err)
	}
}

// ReviveFailedNodes returns long-dead nodes to the queue.
//
// A node that exhausts its retries lands in 'failed', and GetNextPendingNode
// only considers 'pending' and 'completed' — so 'failed' is terminal. That is
// correct for a single node whose service is gone, but it makes an outage
// unrecoverable: if Tor or the network is down for longer than the retry
// budget, every in-flight node burns through its retries and the entire queue
// dies at once, staying dead after the cause is fixed. This is not theoretical
// — it is what happened here between May and July, leaving 23,346 nodes failed,
// nothing pending, and the crawler idle with no way back short of hand-written
// SQL.
//
// `olderThan` is measured from next_crawl_at, which for a failed node is when
// its last scheduled retry would have fired.
//
// Revival is batched to `limit` per pass. Note that this does NOT pace the
// crawler: the per-domain politeness delay is applied when a node is claimed,
// not when it is queued, so reviving everything at once would not make the
// crawl any less polite — it would just produce a very long pending queue. The
// batch exists to keep the UPDATE from locking tens of thousands of rows in one
// transaction, and to make recovery gradual and observable, so a sweep that is
// reviving the wrong thing can be caught and stopped before it has touched the
// entire table.
//
// Because of that, `limit` per hour is unrelated to crawl throughput and the
// pending queue may well grow faster than workers drain it. That is fine — it
// is a work queue — but it means the backlog size is not a sign of trouble on
// its own.
//
// 'blocked' nodes (robots.txt, blacklist) are untouched: those were refused on
// purpose, not by failure.
func (db *DB) ReviveFailedNodes(olderThan time.Duration, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	res, err := db.Conn.Exec(`
		UPDATE nodes
		SET processing_status = 'pending',
		    retry_count       = 0,
		    next_crawl_at     = CURRENT_TIMESTAMP
		WHERE (url, user_id) IN (
			SELECT url, user_id
			FROM nodes
			WHERE processing_status = 'failed'
			  AND next_crawl_at < CURRENT_TIMESTAMP - ($1 || ' seconds')::INTERVAL
			ORDER BY next_crawl_at ASC
			LIMIT $2
		)
	`, fmt.Sprintf("%d", int64(olderThan.Seconds())), limit)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// CountRecentAuthEvents counts events for an already-HMACed subject reference.
// in the last `window` minutes. Used for:
//   - login lockout after 5 consecutive failures ('login_fail')
//   - register rate-limit per recipient email ('register_ok')
func (db *DB) CountRecentAuthEvents(event, emailRef string, windowMinutes int) (int, error) {
	var count int
	err := db.Conn.QueryRow(`
		SELECT COUNT(*) FROM auth_audit
		WHERE event = $1 AND email = $2
		  AND created_at > CURRENT_TIMESTAMP - ($3 || ' minutes')::INTERVAL
	`, event, emailRef, fmt.Sprintf("%d", windowMinutes)).Scan(&count)
	return count, err
}

// PurgeOldAuditLogs deletes events from auth_audit older than `olderThan`.
// Returns the number of deleted rows. Used by the retention job (GDPR + unbounded growth).
func (db *DB) PurgeOldAuditLogs(olderThan time.Duration) (int64, error) {
	res, err := db.Conn.Exec(
		`DELETE FROM auth_audit WHERE created_at < CURRENT_TIMESTAMP - ($1 || ' seconds')::INTERVAL`,
		fmt.Sprintf("%d", int64(olderThan.Seconds())),
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// GlobalStatusCounts returns the number of nodes in each processing_status
// across ALL users. Used by the metrics poller to expose queue depth — it is
// intentionally not user-scoped (operational visibility, not tenant data).
func (db *DB) GlobalStatusCounts() (map[string]int, error) {
	rows, err := db.Conn.Query(`SELECT processing_status, COUNT(*) FROM nodes GROUP BY processing_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

// GetUserAuthInfo returns identity and current authorization state from the DB
// (bypassing JWT claims), plus whether the user still exists. Used by the auth
// middleware to (a) invalidate role demotions immediately and (b) reject tokens
// whose version is stale (revoked via reset/logout-all) or whose user is gone.
func (db *DB) GetUserAuthInfo(userID int) (email, role string, tokenVersion int, found bool, err error) {
	err = db.Conn.QueryRow(`SELECT email, role, token_version FROM users WHERE id = $1`, userID).Scan(&email, &role, &tokenVersion)
	if err == sql.ErrNoRows {
		return "", "", 0, false, nil
	}
	if err != nil {
		return "", "", 0, false, err
	}
	return email, role, tokenVersion, true, nil
}
