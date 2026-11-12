package database

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRecoveryReplacementRollsBackIncompleteSetsAndStaleCredentials(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "recovery@example.com")
	const secret = "JBSWY3DPEHPK3PXP"
	oldHash := RecoveryCodeHash("old-fixture-code")
	if err := db.StageTOTPSecret(uid, 0, secret); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfirmTOTP(uid, 0, secret, 1, []string{oldHash}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateSession(uid, "existing-session", "Test", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	duplicate := RecoveryCodeHash("duplicate-fixture-code")
	for _, hashes := range [][]string{nil, {duplicate, duplicate}} {
		if err := db.RegenerateRecoveryCodes(uid, 1, secret, 2, hashes); err == nil {
			t.Fatal("incomplete or conflicting code set committed")
		}
		state, err := db.GetTOTPState(uid)
		if err != nil || state.LastStep != 1 {
			t.Fatal("failed replacement consumed the authenticator step")
		}
		active, err := db.SessionActive(uid, "existing-session")
		if err != nil || !active {
			t.Fatal("failed replacement revoked a session")
		}
		var count int
		if err := db.Conn.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE user_id=$1 AND code_hash=$2 AND used_at IS NULL`, uid, oldHash).Scan(&count); err != nil || count != 1 {
			t.Fatal("failed replacement discarded old fallback credentials")
		}
	}
	if err := db.RegenerateRecoveryCodes(uid, 0, secret, 2, []string{duplicate}); !errors.Is(err, ErrTOTPReplay) {
		t.Fatal("stale credential version was accepted")
	}
}

func TestConcurrentRecoveryReplacementHasOneWinner(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "concurrent-recovery@example.com")
	const secret = "JBSWY3DPEHPK3PXP"
	if err := db.StageTOTPSecret(uid, 0, secret); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfirmTOTP(uid, 0, secret, 1, []string{RecoveryCodeHash("old-code")}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, code := range []string{"first-new-code", "second-new-code"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- db.RegenerateRecoveryCodes(uid, 1, secret, 2, []string{RecoveryCodeHash(code)})
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded, replayed := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrTOTPReplay):
			replayed++
		default:
			t.Fatal(err)
		}
	}
	if succeeded != 1 || replayed != 1 {
		t.Fatalf("winners=%d rejected=%d", succeeded, replayed)
	}
}
