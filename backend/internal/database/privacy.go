package database

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MaxRetentionDays caps how far out an account may push its retention window.
// Ten years is well past any plausible use for crawl records and keeps the
// cutoff arithmetic away from timestamp overflow.
const MaxRetentionDays = 3650

// ErrRetentionOutOfRange is returned when a caller asks for a retention window
// the schema constraint would refuse anyway.
var ErrRetentionOutOfRange = errors.New("retention window out of range")

// ErrDeletionPending is returned when an account action is refused because the
// account is already scheduled for deletion.
var ErrDeletionPending = errors.New("account deletion already scheduled")

// PrivacySettings is the per-account data policy: how long crawl records live,
// whether page text is stored at all, and whether the account is on its way out.
type PrivacySettings struct {
	RetentionDays        int        `json:"retention_days"`
	StoreContent         bool       `json:"store_content"`
	DeletionRequestedAt  *time.Time `json:"deletion_requested_at"`
	DeletionScheduledFor *time.Time `json:"deletion_scheduled_for"`
}

// GetPrivacySettings reads one account's data policy.
func (db *DB) GetPrivacySettings(userID int) (PrivacySettings, error) {
	var s PrivacySettings
	var requested, scheduled sql.NullTime
	err := db.Conn.QueryRow(`
		SELECT retention_days, store_content, deletion_requested_at, deletion_scheduled_for
		FROM users WHERE id = $1
	`, userID).Scan(&s.RetentionDays, &s.StoreContent, &requested, &scheduled)
	if err != nil {
		return s, err
	}
	if requested.Valid {
		s.DeletionRequestedAt = &requested.Time
	}
	if scheduled.Valid {
		s.DeletionScheduledFor = &scheduled.Time
	}
	return s, nil
}

// SetPrivacySettings writes the account's retention window and content-storage
// preference.
//
// Turning store_content off stops new page text from being written but does not
// erase what is already stored — that is PurgeStoredContent's job, offered
// alongside it in the interface. Bundling the two would mean a settings toggle
// silently destroyed data, and an account that only wants the change to apply
// going forward would have no way to ask for it.
func (db *DB) SetPrivacySettings(userID, retentionDays int, storeContent bool) error {
	if retentionDays < 0 || retentionDays > MaxRetentionDays {
		return ErrRetentionOutOfRange
	}
	_, err := db.Conn.Exec(
		`UPDATE users SET retention_days = $2, store_content = $3 WHERE id = $1`,
		userID, retentionDays, storeContent,
	)
	return err
}

// RequestAccountDeletion schedules the account for erasure after a grace period
// and returns the moment it becomes due.
//
// Nothing is destroyed here. The delay is the point: deletion is the one action
// in this application that cannot be undone, so it is made reversible for as
// long as the operator configures. A second request while one is pending is
// refused rather than silently restarting the clock — that would let anyone
// holding a session keep pushing the deadline around.
func (db *DB) RequestAccountDeletion(userID int, grace time.Duration) (time.Time, error) {
	if grace < 0 {
		grace = 0
	}
	var scheduled time.Time
	err := db.Conn.QueryRow(`
		UPDATE users
		   SET deletion_requested_at  = CURRENT_TIMESTAMP,
		       deletion_scheduled_for = CURRENT_TIMESTAMP + ($2 * INTERVAL '1 second')
		 WHERE id = $1 AND deletion_scheduled_for IS NULL
		RETURNING deletion_scheduled_for
	`, userID, int64(grace.Seconds())).Scan(&scheduled)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrDeletionPending
	}
	return scheduled, err
}

// CancelAccountDeletion clears a pending deletion. Reports whether one was
// actually pending, so a stale interface can be told the truth rather than
// shown a success it did not cause.
//
// Deliberately not re-authenticated: cancelling only ever preserves data, and
// putting a password prompt in front of the undo button is exactly the friction
// that turns a misclick into a permanent loss.
func (db *DB) CancelAccountDeletion(userID int) (bool, error) {
	res, err := db.Conn.Exec(`
		UPDATE users
		   SET deletion_requested_at = NULL, deletion_scheduled_for = NULL
		 WHERE id = $1 AND deletion_scheduled_for IS NOT NULL
	`, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DueDeletion is an account whose grace period has elapsed.
type DueDeletion struct {
	UserID int
	Email  string
}

// DueAccountDeletions lists accounts the sweeper should now erase, oldest
// request first, bounded so one run cannot monopolise the database.
func (db *DB) DueAccountDeletions(limit int) ([]DueDeletion, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Conn.Query(`
		SELECT id, email FROM users
		 WHERE deletion_scheduled_for IS NOT NULL
		   AND deletion_scheduled_for <= CURRENT_TIMESTAMP
		 ORDER BY deletion_scheduled_for
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var due []DueDeletion
	for rows.Next() {
		var d DueDeletion
		if err := rows.Scan(&d.UserID, &d.Email); err != nil {
			return nil, err
		}
		due = append(due, d)
	}
	return due, rows.Err()
}

// DeleteAccount erases an account and everything attached to it.
//
// nodes, edges, sessions and recovery_codes all carry an ON DELETE CASCADE
// reference to users, so removing the row removes them. auth_audit does not: it
// is keyed by a keyed reference of the address rather than by user_id, precisely
// so that table never becomes a directory of who logged in from where. That
// makes it invisible to the cascade, so the caller supplies the same reference
// and it is purged in the same transaction — an account is not deleted while its
// login history survives.
func (db *DB) DeleteAccount(userID int, auditEmailRef string) error {
	tx, err := db.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if auditEmailRef != "" {
		if _, err := tx.Exec(`DELETE FROM auth_audit WHERE email = $1`, auditEmailRef); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteCrawlHistory removes every node an account owns, and with them (by
// cascade) every edge. Returns how many nodes went.
func (db *DB) DeleteCrawlHistory(userID int) (int64, error) {
	res, err := db.Conn.Exec(`DELETE FROM nodes WHERE user_id = $1`, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgeStoredContent drops the retained copy of every page an account crawled,
// keeping the record that the page exists.
//
// content_hash is cleared alongside it. The hash is derived from the text, so
// leaving it behind would make the next crawl compare fresh content against a
// digest of a copy we no longer hold and conclude nothing had changed — the row
// would then never regain content even after the account turned storage back on.
func (db *DB) PurgeStoredContent(userID int) (int64, error) {
	res, err := db.Conn.Exec(`
		UPDATE nodes SET content = NULL, content_hash = NULL
		 WHERE user_id = $1 AND content IS NOT NULL AND content <> ''
	`, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgeAuthAudit deletes an account's authentication history on request.
//
// Recent throttling events are held back. auth_audit is not only a record — it
// is also the state behind the account lockout after repeated failures and the
// per-recipient caps on verification and reset mail. Deleting it wholesale on
// demand would turn "clear my history" into a reset button for those counters,
// letting one account send several times its share of mail per hour simply by
// purging between attempts. The carve-out is two hours, comfortably past the
// longest window any of those checks looks back over, so everything the user
// would recognise as their history still goes and only the live counters
// survive — and only until they expire on their own.
func (db *DB) PurgeAuthAudit(auditEmailRef string) (int64, error) {
	if auditEmailRef == "" {
		return 0, nil
	}
	res, err := db.Conn.Exec(`
		DELETE FROM auth_audit
		 WHERE email = $1
		   AND NOT (event IN ('login_fail', 'register_ok', 'reset_request')
		            AND created_at > CURRENT_TIMESTAMP - INTERVAL '2 hours')
	`, auditEmailRef)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RetentionReport summarises one pass of the retention sweeper.
type RetentionReport struct {
	Accounts     int   `json:"accounts"`
	NodesMatched int64 `json:"nodes_matched"`
	NodesDeleted int64 `json:"nodes_deleted"`
	DryRun       bool  `json:"dry_run"`
}

// ApplyRetention deletes crawl records that have outlived the policy of the
// account that owns them.
//
// batch bounds the work per account per pass rather than per run: a backlog
// drains over several passes instead of holding a delete of unknown size open
// across the whole table. dryRun reports what would go without touching
// anything, which is the only responsible way to introduce an automatic
// destructive job to a database that already has data in it.
//
// Rows being crawled right now are excluded. A worker holds the URL in flight
// and will write results back for it; deleting the row underneath produces a
// crawl whose output lands nowhere and a log line about a node that no longer
// exists. It will be reaped on the next pass, once it is no longer in flight.
//
// So are rows the account has annotated or is watching. A retention window is a
// statement about crawl records the account has not looked at; a tag, a note or
// a watch is that account explicitly saying this site matters to it. Reaping
// those on a timer would quietly destroy the user's own writing — which the
// crawl data cannot regenerate — as a side effect of a setting about crawl data.
func (db *DB) ApplyRetention(ctx context.Context, batch int, dryRun bool) (RetentionReport, error) {
	report := RetentionReport{DryRun: dryRun}
	if batch <= 0 {
		batch = 1000
	}

	rows, err := db.Conn.QueryContext(ctx,
		`SELECT id, retention_days FROM users WHERE retention_days > 0 ORDER BY id`)
	if err != nil {
		return report, err
	}
	type policy struct {
		userID, days int
	}
	var policies []policy
	for rows.Next() {
		var p policy
		if err := rows.Scan(&p.userID, &p.days); err != nil {
			rows.Close()
			return report, err
		}
		policies = append(policies, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return report, err
	}

	for _, p := range policies {
		report.Accounts++
		cutoff := time.Now().AddDate(0, 0, -p.days)

		if dryRun {
			var n int64
			err := db.Conn.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM nodes
				 WHERE user_id = $1
				   AND discovered_at < $2
				   AND (last_crawled_at IS NULL OR last_crawled_at < $2)
				   AND processing_status <> 'crawling'
				   AND NOT EXISTS (SELECT 1 FROM node_tags  t WHERE t.node_id = nodes.id)
				   AND NOT EXISTS (SELECT 1 FROM node_notes o WHERE o.node_id = nodes.id)
				   AND NOT EXISTS (SELECT 1 FROM watches    w WHERE w.node_id = nodes.id)
			`, p.userID, cutoff).Scan(&n)
			if err != nil {
				return report, err
			}
			report.NodesMatched += n
			continue
		}

		res, err := db.Conn.ExecContext(ctx, `
			DELETE FROM nodes WHERE id IN (
				SELECT id FROM nodes
				 WHERE user_id = $1
				   AND discovered_at < $2
				   AND (last_crawled_at IS NULL OR last_crawled_at < $2)
				   AND processing_status <> 'crawling'
				   AND NOT EXISTS (SELECT 1 FROM node_tags  t WHERE t.node_id = nodes.id)
				   AND NOT EXISTS (SELECT 1 FROM node_notes o WHERE o.node_id = nodes.id)
				   AND NOT EXISTS (SELECT 1 FROM watches    w WHERE w.node_id = nodes.id)
				 ORDER BY discovered_at
				 LIMIT $3
			)
		`, p.userID, cutoff, batch)
		if err != nil {
			return report, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return report, err
		}
		report.NodesMatched += n
		report.NodesDeleted += n
	}
	return report, nil
}

// AuthEvent is one entry from an account's authentication history, as returned
// in a personal-data export.
//
// Neither the email nor the address is included: auth_audit stores keyed
// references rather than the values themselves, and re-deriving them for an
// export would defeat the reason they were never stored in the first place. The
// account already knows its own address; what it cannot otherwise see is when
// and how its credentials were used.
type AuthEvent struct {
	Event     string `json:"event"`
	CreatedAt string `json:"created_at"`
}

// ListAuthEvents returns an account's authentication history, newest first.
func (db *DB) ListAuthEvents(auditEmailRef string, limit int) ([]AuthEvent, error) {
	if auditEmailRef == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := db.Conn.Query(`
		SELECT event, COALESCE(TO_CHAR(created_at, 'YYYY-MM-DD"T"HH24:MI:SS'), '')
		  FROM auth_audit WHERE email = $1
		 ORDER BY created_at DESC LIMIT $2
	`, auditEmailRef, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []AuthEvent
	for rows.Next() {
		var e AuthEvent
		if err := rows.Scan(&e.Event, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// ExportEdges streams every link an account discovered, for personal-data
// export. Streamed rather than collected because an account's graph can be
// far larger than the response it fits in comfortably.
func (db *DB) ExportEdges(ctx context.Context, userID int, fn func(Edge) error) error {
	rows, err := db.Conn.QueryContext(ctx,
		`SELECT source_url, target_url FROM edges WHERE user_id = $1 ORDER BY discovered_at`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.Source, &e.Target); err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

// AccountProfile is the account's own record, as returned in an export.
type AccountProfile struct {
	ID              int    `json:"id"`
	Email           string `json:"email"`
	Role            string `json:"role"`
	IsVerified      bool   `json:"is_verified"`
	CreatedAt       string `json:"created_at"`
	TOTPEnabled     bool   `json:"totp_enabled"`
	TOTPConfirmedAt string `json:"totp_confirmed_at,omitempty"`
}

// GetAccountProfile returns the stored account record.
//
// Credential material is not selected at all: the password digest, the TOTP
// seed, recovery-code digests and the opaque verification and reset tokens are
// all excluded. An export is meant to show what is held about someone, and
// putting live credentials into a file that will be downloaded, synced and
// mailed around would create a far worse exposure than the transparency is
// worth.
func (db *DB) GetAccountProfile(userID int) (AccountProfile, error) {
	var p AccountProfile
	var confirmed sql.NullString
	err := db.Conn.QueryRow(`
		SELECT id, email, COALESCE(role, 'user'), COALESCE(is_verified, FALSE),
		       COALESCE(TO_CHAR(created_at, 'YYYY-MM-DD"T"HH24:MI:SS'), ''),
		       totp_enabled,
		       TO_CHAR(totp_confirmed_at, 'YYYY-MM-DD"T"HH24:MI:SS')
		  FROM users WHERE id = $1
	`, userID).Scan(&p.ID, &p.Email, &p.Role, &p.IsVerified, &p.CreatedAt, &p.TOTPEnabled, &confirmed)
	if err != nil {
		return p, err
	}
	p.TOTPConfirmedAt = confirmed.String
	return p, nil
}
