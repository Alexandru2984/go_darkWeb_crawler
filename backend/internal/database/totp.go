package database

import (
	"database/sql"
	"errors"
	"fmt"
)

// ErrTOTPReplay means the presented code was valid but is not newer than the
// last one already accepted for this account.
var ErrTOTPReplay = errors.New("one-time code already used")

// ErrTOTPStateChanged means the credentials or enrolment changed before the write.
var ErrTOTPStateChanged = errors.New("two-factor enrolment state changed")

// TOTPState is the second-factor configuration for one account.
type TOTPState struct {
	Secret    string `json:"-"`
	Enabled   bool
	LastStep  int64
	Confirmed bool
}

// GetTOTPState reads an account's second-factor configuration.
func (db *DB) GetTOTPState(userID int) (TOTPState, error) {
	var (
		s        TOTPState
		secret   sql.NullString
		confirat sql.NullTime
	)
	err := db.Conn.QueryRow(
		`SELECT totp_secret, totp_enabled, totp_last_step, totp_confirmed_at FROM users WHERE id = $1`,
		userID,
	).Scan(&secret, &s.Enabled, &s.LastStep, &confirat)
	if err != nil {
		return s, err
	}
	if secret.Valid {
		if db.totpKeys == nil {
			return s, errTOTPEncryption
		}
		s.Secret, err = db.openTOTPSecret(userID, secret.String)
		if err != nil {
			return TOTPState{}, err
		}
	}
	s.Confirmed = confirat.Valid
	return s, nil
}

// StageTOTPSecret stores a secret for an enrolment that has not been confirmed
// yet. It refuses to touch an account whose second factor is already active, so
// a stolen session cannot quietly swap the authenticator for its own.
func (db *DB) StageTOTPSecret(userID, tokenVersion int, secret string) error {
	if db.totpKeys == nil {
		return errTOTPEncryption
	}
	sealed, err := db.totpKeys.Seal(userID, secret)
	if err != nil {
		return err
	}
	res, err := db.Conn.Exec(
		`UPDATE users SET totp_secret = $1, totp_confirmed_at = NULL
		 WHERE id = $2 AND totp_enabled = FALSE AND token_version = $3`,
		sealed, userID, tokenVersion,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTOTPStateChanged
	}
	return nil
}

// ConfirmTOTP activates the second factor and replaces any recovery codes in
// one transaction, so an account can never end up enrolled with no way back in.
// step is the counter of the code that proved enrolment and is recorded
// immediately, so that same code cannot then be replayed at login.
func (db *DB) ConfirmTOTP(userID, tokenVersion int, secret string, step int64, codeHashes []string) error {
	tx, err := db.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stored, err := db.matchingTOTPSecret(tx, userID, secret)
	if err != nil {
		return err
	}

	res, err := tx.Exec(
		`UPDATE users
		 SET totp_enabled = TRUE, totp_confirmed_at = clock_timestamp(),
		     totp_last_step = $1, token_version = token_version + 1
		 WHERE id = $2 AND totp_enabled = FALSE AND totp_secret = $3 AND token_version = $4`,
		step, userID, stored, tokenVersion,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTOTPStateChanged
	}

	if _, err := tx.Exec(`DELETE FROM recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, h := range codeHashes {
		if _, err := tx.Exec(
			`INSERT INTO recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, h,
		); err != nil {
			return err
		}
	}
	if err := revokeTOTPSessions(tx, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumeTOTPStep records that a code was accepted, and reports ErrTOTPReplay if
// an equal or older step was already used.
//
// The comparison and the write are one statement, so two requests presenting
// the same code concurrently cannot both find it unused. A read-then-write
// would let both pass: the second factor would stop being single-use exactly
// when someone is racing it.
func (db *DB) ConsumeTOTPStep(userID int, secret string, step int64) error {
	tx, err := db.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stored, err := db.matchingTOTPSecret(tx, userID, secret)
	if errors.Is(err, ErrTOTPStateChanged) {
		return ErrTOTPReplay
	}
	if err != nil {
		return err
	}
	res, err := tx.Exec(
		`UPDATE users SET totp_last_step = $1 WHERE id = $2 AND totp_last_step < $1 AND totp_enabled AND totp_secret = $3`,
		step, userID, stored,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTOTPReplay
	}
	return tx.Commit()
}

// ConsumeRecoveryCode marks a recovery code used and reports whether it was
// valid and unused. Marking happens in the same statement that matches it, for
// the same reason as ConsumeTOTPStep.
func (db *DB) ConsumeRecoveryCode(userID int, codeHash string) (bool, error) {
	res, err := db.Conn.Exec(
		`UPDATE recovery_codes SET used_at = CURRENT_TIMESTAMP
		 WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`,
		userID, codeHash,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// CountUnusedRecoveryCodes lets the UI warn before the last fallback is gone.
func (db *DB) CountUnusedRecoveryCodes(userID int) (int, error) {
	var n int
	err := db.Conn.QueryRow(
		`SELECT COUNT(*) FROM recovery_codes WHERE user_id = $1 AND used_at IS NULL`, userID,
	).Scan(&n)
	return n, err
}

// DisableTOTP removes the second factor and every recovery code. The caller is
// responsible for re-authenticating the user first: disabling is as sensitive
// as enrolling.
func (db *DB) DisableTOTP(userID, tokenVersion int) error {
	tx, err := db.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`UPDATE users SET totp_secret = NULL, totp_enabled = FALSE,
		 totp_confirmed_at = NULL, totp_last_step = 0, token_version = token_version + 1
		 WHERE id = $1 AND token_version = $2 AND totp_enabled`, userID, tokenVersion,
	)
	if err != nil {
		return fmt.Errorf("clear totp: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTOTPStateChanged
	}
	if _, err := tx.Exec(`DELETE FROM recovery_codes WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear recovery codes: %w", err)
	}
	if err := revokeTOTPSessions(tx, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func revokeTOTPSessions(tx *sql.Tx, userID int) error {
	_, err := tx.Exec(`UPDATE sessions SET revoked_at = CURRENT_TIMESTAMP
		WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

// RecoveryCodeHash is the only representation of a recovery code that may be
// persisted. The codes carry crypto/rand entropy, so a digest is enough.
func RecoveryCodeHash(code string) string {
	return opaqueTokenHash(code)
}
