package database

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPasswordChangeInvalidatesSessionsAndPendingCredentials(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "change@example.com")
	other := mustUser(t, db, "bystander@example.com")
	if _, err := db.SetResetToken("change@example.com", "old-reset-token-123456789"); err != nil {
		t.Fatal(err)
	}
	if err := db.StageTOTPSecret(uid, 0, "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{uid, uid, other} {
		if _, err := db.CreateSession(id, time.Now().String(), "Test", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ChangePassword(uid, 0, "wrong-hash", "new-hash"); !errors.Is(err, ErrCredentialsChanged) {
		t.Fatalf("wrong prior hash: %v", err)
	}
	if err := db.ChangePassword(uid, 0, "x", "new-hash"); err != nil {
		t.Fatal(err)
	}
	var hash string
	var version, active, bystanderActive int
	var resetMissing, pendingMissing bool
	if err := db.Conn.QueryRow(`SELECT password_hash, token_version, reset_token IS NULL AND reset_expires_at IS NULL,
		totp_secret IS NULL FROM users WHERE id=$1`, uid).Scan(&hash, &version, &resetMissing, &pendingMissing); err != nil {
		t.Fatal(err)
	}
	if hash != "new-hash" || version != 1 || !resetMissing || !pendingMissing {
		t.Fatalf("password/reset/enrolment state was not updated atomically")
	}
	if err := db.Conn.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL`, uid).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL`, other).Scan(&bystanderActive); err != nil {
		t.Fatal(err)
	}
	if active != 0 || bystanderActive != 1 {
		t.Fatalf("active sessions changed=%d bystander=%d", active, bystanderActive)
	}
	if err := db.ResetPassword("old-reset-token-123456789", "reset-hash"); err == nil {
		t.Fatal("pre-change reset token remained usable")
	}
}

func TestConcurrentPasswordChangesOnlyOneCommits(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "race@example.com")
	var wait sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, hash := range []string{"first", "second"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- db.ChangePassword(uid, 0, "x", hash)
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrCredentialsChanged):
			conflicted++
		default:
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("success=%d conflicts=%d", succeeded, conflicted)
	}
}
