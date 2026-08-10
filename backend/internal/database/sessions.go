package database

import "time"

// Session is one signed-in device, as shown to the account that owns it.
type Session struct {
	ID          int    `json:"id"`
	DeviceLabel string `json:"device_label"`
	CreatedAt   string `json:"created_at"`
	LastUsedAt  string `json:"last_used_at"`
	ExpiresAt   string `json:"expires_at"`
	Current     bool   `json:"current"`
}

// CreateSession records a new signed-in device and returns its row ID.
func (db *DB) CreateSession(userID int, tokenID, deviceLabel string, expiresAt time.Time) (int, error) {
	var id int
	err := db.Conn.QueryRow(
		`INSERT INTO sessions (user_id, token_id, device_label, expires_at)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		userID, tokenID, deviceLabel, expiresAt,
	).Scan(&id)
	return id, err
}

// SessionActive reports whether a token's session is still usable: present, not
// revoked, not expired, and belonging to the user the token claims.
//
// The user_id condition is not redundant. Without it, a token whose identity
// claim was somehow mismatched with its session handle would still resolve, and
// the session table would stop being an authorization boundary.
func (db *DB) SessionActive(userID int, tokenID string) (bool, error) {
	var exists bool
	err := db.Conn.QueryRow(
		`SELECT EXISTS (
			SELECT 1 FROM sessions
			WHERE token_id = $1 AND user_id = $2
			  AND revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP
		)`, tokenID, userID,
	).Scan(&exists)
	return exists, err
}

// TouchSession refreshes last_used_at, but only when the stored value is
// already stale.
//
// staleAfter exists to keep this off the hot path: writing on every request
// would turn each read into a write and put a row update behind every API call.
// A few minutes of imprecision in "last used" costs the user nothing.
func (db *DB) TouchSession(tokenID string, staleAfter time.Duration) error {
	_, err := db.Conn.Exec(
		`UPDATE sessions SET last_used_at = CURRENT_TIMESTAMP
		 WHERE token_id = $1 AND revoked_at IS NULL
		   AND last_used_at < CURRENT_TIMESTAMP - $2::interval`,
		tokenID, staleAfter.String(),
	)
	return err
}

// ListSessions returns an account's live sessions, newest activity first.
// currentTokenID marks which row is the caller's own, so the UI can avoid
// inviting someone to revoke the session they are using without warning.
func (db *DB) ListSessions(userID int, currentTokenID string) ([]Session, error) {
	rows, err := db.Conn.Query(
		`SELECT id,
		        COALESCE(device_label, ''),
		        to_char(created_at,   'YYYY-MM-DD HH24:MI:SS'),
		        to_char(last_used_at, 'YYYY-MM-DD HH24:MI:SS'),
		        to_char(expires_at,   'YYYY-MM-DD HH24:MI:SS'),
		        token_id = $2
		 FROM sessions
		 WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP
		 ORDER BY last_used_at DESC`,
		userID, currentTokenID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := []Session{}
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.DeviceLabel, &s.CreatedAt, &s.LastUsedAt, &s.ExpiresAt, &s.Current); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// RevokeSession ends one session. The user_id condition is what stops a caller
// revoking somebody else's session by guessing a row ID.
func (db *DB) RevokeSession(userID, sessionID int) (bool, error) {
	res, err := db.Conn.Exec(
		`UPDATE sessions SET revoked_at = CURRENT_TIMESTAMP
		 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		sessionID, userID,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RevokeAllSessions ends every session for an account, optionally sparing one
// (the caller's own, when the intent is "sign out my other devices").
func (db *DB) RevokeAllSessions(userID int, exceptTokenID string) error {
	_, err := db.Conn.Exec(
		`UPDATE sessions SET revoked_at = CURRENT_TIMESTAMP
		 WHERE user_id = $1 AND revoked_at IS NULL AND ($2 = '' OR token_id <> $2)`,
		userID, exceptTokenID,
	)
	return err
}

// RevokeSessionByToken ends the session behind one token, used at logout.
func (db *DB) RevokeSessionByToken(tokenID string) error {
	_, err := db.Conn.Exec(
		`UPDATE sessions SET revoked_at = CURRENT_TIMESTAMP
		 WHERE token_id = $1 AND revoked_at IS NULL`, tokenID)
	return err
}

// DeleteExpiredSessions removes rows no token can refer to any more. Revoked
// rows are kept until their natural expiry so the inventory stays honest for
// the lifetime a credential could have been replayed.
func (db *DB) DeleteExpiredSessions() (int64, error) {
	res, err := db.Conn.Exec(`DELETE FROM sessions WHERE expires_at < CURRENT_TIMESTAMP`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
