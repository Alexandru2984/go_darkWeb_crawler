package database

import "errors"

// ErrCredentialsChanged rejects a write based on credentials that changed
// after re-authentication, including concurrent password resets or MFA changes.
var ErrCredentialsChanged = errors.New("account credentials changed")

// ChangePassword changes only the authenticated account. The version and hash
// comparison prevent a concurrent reset/change from being overwritten. Session
// revocation and reset-token invalidation commit with the new password.
func (db *DB) ChangePassword(userID, tokenVersion int, oldHash, newHash string) error {
	tx, err := db.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`UPDATE users
		SET password_hash = $1, token_version = token_version + 1,
		    reset_token = NULL, reset_expires_at = NULL,
		    totp_secret = CASE WHEN totp_enabled THEN totp_secret ELSE NULL END
		WHERE id = $2 AND token_version = $3 AND password_hash = $4`,
		newHash, userID, tokenVersion, oldHash)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrCredentialsChanged
	}
	if err := revokeTOTPSessions(tx, userID); err != nil {
		return err
	}
	return tx.Commit()
}
