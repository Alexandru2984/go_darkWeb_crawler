package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

var (
	ErrInvitationUnavailable = errors.New("only pending user accounts can be invited")
	ErrInvitationCooldown    = errors.New("wait five minutes before sending another invitation")
	ErrActivationInvalid     = errors.New("activation link invalid, expired or already used")
)

// AdminUser deliberately excludes credential hashes, seeds and link tokens.
type AdminUser struct {
	ID         int       `json:"id"`
	Email      string    `json:"email"`
	Role       string    `json:"role"`
	IsVerified bool      `json:"is_verified"`
	MFAEnabled bool      `json:"mfa_enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

func (db *DB) ListAdminUsers(ctx context.Context, limit, offset int) ([]AdminUser, error) {
	rows, err := db.Conn.QueryContext(ctx, `SELECT id, email, role, is_verified, totp_enabled, created_at
		FROM users ORDER BY id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]AdminUser, 0)
	for rows.Next() {
		var user AdminUser
		if err := rows.Scan(&user.ID, &user.Email, &user.Role, &user.IsVerified, &user.MFAEnabled, &user.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// Lock the actor through commit: a concurrent role/password/MFA change must
// either precede this check (and reject it), or follow the completed action.
func lockInvitationActor(ctx context.Context, tx *sql.Tx, actorID, version int, requireMFA bool) error {
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT role = 'admin' AND token_version = $2 AND (NOT $3 OR totp_enabled)
		FROM users WHERE id = $1 FOR UPDATE`, actorID, version, requireMFA).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !allowed) {
		return ErrCredentialsChanged
	}
	return err
}

// CreateInvitedUser never grants a role supplied by the client. The initial
// password is random and unknown; the recipient chooses their own via email.
func (db *DB) CreateInvitedUser(ctx context.Context, actorID, version int, requireMFA bool, email, passwordHash, token string) (int, error) {
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := lockInvitationActor(ctx, tx, actorID, version, requireMFA); err != nil {
		return 0, err
	}
	var id int
	err = tx.QueryRowContext(ctx, `INSERT INTO users (email, password_hash, role, verification_token, verification_expires_at)
		VALUES ($1, $2, 'user', $3, CURRENT_TIMESTAMP + INTERVAL '24 hours') RETURNING id`,
		NormalizeEmail(email), passwordHash, opaqueTokenHash(token)).Scan(&id)
	if err != nil {
		var pg *pq.Error
		if errors.As(err, &pg) && pg.Code == "23505" {
			return 0, ErrEmailInUse
		}
		return 0, err
	}
	return id, tx.Commit()
}

// RefreshInvitation invalidates the old link atomically. The timestamp guard
// enforces a recipient cooldown even across concurrent API instances.
func (db *DB) RefreshInvitation(ctx context.Context, actorID, version int, requireMFA bool, targetID int, token string) (string, error) {
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := lockInvitationActor(ctx, tx, actorID, version, requireMFA); err != nil {
		return "", err
	}
	var email, role string
	var verified bool
	var expires sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT email, role, is_verified, verification_expires_at FROM users WHERE id = $1 FOR UPDATE`, targetID).
		Scan(&email, &role, &verified, &expires)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (role != "user" || verified)) {
		return "", ErrInvitationUnavailable
	}
	if err != nil {
		return "", err
	}
	var cooling bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE($1::timestamp > CURRENT_TIMESTAMP + INTERVAL '23 hours 55 minutes', FALSE)`, expires).Scan(&cooling); err != nil {
		return "", err
	}
	if cooling {
		return "", ErrInvitationCooldown
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET verification_token = $2,
		verification_expires_at = CURRENT_TIMESTAMP + INTERVAL '24 hours' WHERE id = $1`, targetID, opaqueTokenHash(token)); err != nil {
		return "", err
	}
	return email, tx.Commit()
}

// ActivateInvitedUser sets the password and verifies the address in one write.
// Only a hash of the email token reaches PostgreSQL; replay cannot succeed.
func (db *DB) ActivateInvitedUser(ctx context.Context, token, newHash string) error {
	result, err := db.Conn.ExecContext(ctx, `UPDATE users SET password_hash = $2, is_verified = TRUE,
		verification_token = NULL, verification_expires_at = NULL,
		reset_token = NULL, reset_expires_at = NULL, token_version = token_version + 1
		WHERE verification_token = $1 AND role = 'user' AND is_verified = FALSE
		AND verification_expires_at > CURRENT_TIMESTAMP`, opaqueTokenHash(token), newHash)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrActivationInvalid
	}
	return nil
}
