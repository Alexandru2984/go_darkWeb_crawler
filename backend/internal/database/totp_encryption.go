package database

import (
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"errors"
	"log/slog"
	"strings"

	"onion-spider/internal/secrets"
)

var errTOTPEncryption = errors.New("TOTP encryption is not configured")

// ConfigureTOTPEncryption runs before serving requests. Every existing envelope
// is authenticated before the transaction commits. Legacy seeds and envelopes
// using retained keys are re-encrypted with the active key, under row locks.
// A missing key or corrupt record aborts the whole migration, without disabling
// MFA, deleting recovery codes or resetting the replay watermark.
func (db *DB) ConfigureTOTPEncryption(keys *secrets.Keyring) error {
	return db.configureTOTPStorage(keys, true)
}

// ConfigureTOTPCompatibility provides the first deployment's rollback mode:
// authenticate both old and new records, encrypt every new write, and leave
// existing records unchanged. Normal startup always migrates legacy records.
func (db *DB) ConfigureTOTPCompatibility(keys *secrets.Keyring) error {
	return db.configureTOTPStorage(keys, false)
}

func (db *DB) configureTOTPStorage(keys *secrets.Keyring, migrate bool) error {
	if keys == nil {
		return errTOTPEncryption
	}
	tx, err := db.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(790021609)`); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id, totp_secret FROM users WHERE totp_secret IS NOT NULL ORDER BY id FOR UPDATE`)
	if err != nil {
		return err
	}
	type replacement struct {
		id     int
		sealed string
	}
	var changes []replacement
	for rows.Next() {
		var id int
		var stored string
		if err := rows.Scan(&id, &stored); err != nil {
			rows.Close()
			return err
		}
		plain := stored
		if strings.HasPrefix(stored, "totp:") {
			plain, err = keys.Open(id, stored)
		} else {
			decoded, decodeErr := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(stored)
			if decodeErr != nil || len(decoded) < 10 || len(stored) > 128 {
				err = secrets.ErrDecrypt
			}
		}
		if err != nil {
			rows.Close()
			return err
		}
		if migrate && !keys.UsesActiveKey(stored) {
			sealed, err := keys.Seal(id, plain)
			if err != nil {
				rows.Close()
				return err
			}
			changes = append(changes, replacement{id, sealed})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, change := range changes {
		if _, err := tx.Exec(`UPDATE users SET totp_secret=$1 WHERE id=$2`, change.sealed, change.id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	db.totpKeys = keys
	db.totpLegacyReads = !migrate
	slog.Info("totp_encryption_ready", "rewrapped_secrets", len(changes), "compatibility", !migrate)
	return nil
}

// matchingTOTPSecret authenticates a locked record and compares it in memory.
// Plain seeds never become PostgreSQL query parameters. The lock serializes
// enrolment replacement, password changes, replay consumption and key rotation.
func (db *DB) matchingTOTPSecret(tx *sql.Tx, userID int, expected string) (string, error) {
	if db.totpKeys == nil {
		return "", errTOTPEncryption
	}
	var stored sql.NullString
	if err := tx.QueryRow(`SELECT totp_secret FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&stored); err != nil {
		return "", err
	}
	if !stored.Valid {
		return "", ErrTOTPStateChanged
	}
	plain, err := db.openTOTPSecret(userID, stored.String)
	if err != nil {
		return "", err
	}
	if subtle.ConstantTimeCompare([]byte(plain), []byte(expected)) != 1 {
		return "", ErrTOTPStateChanged
	}
	return stored.String, nil
}

func (db *DB) openTOTPSecret(userID int, stored string) (string, error) {
	if db.totpKeys == nil {
		return "", errTOTPEncryption
	}
	if db.totpLegacyReads && !strings.HasPrefix(stored, "totp:") {
		decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(stored)
		if err == nil && len(decoded) >= 10 && len(stored) <= 128 {
			return stored, nil
		}
		return "", secrets.ErrDecrypt
	}
	return db.totpKeys.Open(userID, stored)
}
