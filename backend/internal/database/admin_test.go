package database

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestInvitationGuardsAndActivationExpiry(t *testing.T) {
	db := newTestDB(t)
	actor := mustUser(t, db, "owner@example.com")
	if _, err := db.Conn.Exec(`UPDATE users SET role='admin', totp_enabled=TRUE WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"role='user'", "token_version=1", "totp_enabled=FALSE"} {
		if _, err := db.Conn.Exec(`UPDATE users SET `+change+` WHERE id=$1`, actor); err != nil {
			t.Fatal(err)
		}
		if _, err := db.CreateInvitedUser(t.Context(), actor, 0, true, "refused@example.com", "hash", strings.Repeat("a", 64)); !errors.Is(err, ErrCredentialsChanged) {
			t.Fatalf("stale actor accepted after %s: %v", change, err)
		}
		if _, err := db.Conn.Exec(`UPDATE users SET role='admin', token_version=0, totp_enabled=TRUE WHERE id=$1`, actor); err != nil {
			t.Fatal(err)
		}
	}
	token := strings.Repeat("b", 64)
	id, err := db.CreateInvitedUser(t.Context(), actor, 0, true, "pending@example.com", "initial-hash", token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetResetToken("pending@example.com", "old-reset-token-123456789"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn.Exec(`UPDATE users SET verification_expires_at=CURRENT_TIMESTAMP - INTERVAL '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := db.ActivateInvitedUser(t.Context(), token, "expired-hash"); !errors.Is(err, ErrActivationInvalid) {
		t.Fatal("expired invitation activated")
	}
	if _, err := db.Conn.Exec(`UPDATE users SET verification_expires_at=CURRENT_TIMESTAMP + INTERVAL '1 hour' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := db.ActivateInvitedUser(t.Context(), token, "chosen-hash"); err != nil {
		t.Fatal(err)
	}
	if err := db.ResetPassword("old-reset-token-123456789", "old-reset-hash"); err == nil {
		t.Fatal("pre-activation reset token remained valid")
	}
	u, _ := db.GetUserByEmail("pending@example.com")
	if !u.IsVerified || u.TokenVersion != 1 || u.PasswordHash != "chosen-hash" {
		t.Fatal("activation was incomplete")
	}
	if _, err := db.RefreshInvitation(t.Context(), actor, 0, true, actor, strings.Repeat("c", 64)); !errors.Is(err, ErrInvitationUnavailable) {
		t.Fatal("administrator got invitation")
	}
}

func TestConcurrentInvitationResendsOnlyOneCommits(t *testing.T) {
	db := newTestDB(t)
	actor := mustUser(t, db, "owner@example.com")
	if _, err := db.Conn.Exec(`UPDATE users SET role='admin' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	id, err := db.CreateInvitedUser(t.Context(), actor, 0, false, "pending@example.com", "hash", strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn.Exec(`UPDATE users SET verification_expires_at=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, token := range []string{strings.Repeat("e", 64), strings.Repeat("f", 64)} {
		wait.Go(func() {
			<-start
			_, err := db.RefreshInvitation(t.Context(), actor, 0, false, id, token)
			results <- err
		})
	}
	close(start)
	wait.Wait()
	close(results)
	ok, cooldown := 0, 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrInvitationCooldown):
			cooldown++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || cooldown != 1 {
		t.Fatalf("resend race: success=%d cooldown=%d", ok, cooldown)
	}
}
