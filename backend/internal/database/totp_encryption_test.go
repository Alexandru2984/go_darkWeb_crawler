package database

import (
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"onion-spider/internal/secrets"
)

func TestTOTPLegacyMigrationRotationAndReplayState(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "encrypted@example.com")
	const seed = "JBSWY3DPEHPK3PXP"
	if _, err := db.Conn.Exec(`UPDATE users SET totp_secret=$1, totp_enabled=TRUE, totp_last_step=21,
		totp_confirmed_at=CURRENT_TIMESTAMP WHERE id=$2`, seed, uid); err != nil {
		t.Fatal(err)
	}
	oldKey, newKey := make([]byte, 32), make([]byte, 32)
	for _, key := range [][]byte{oldKey, newKey} {
		if _, err := rand.Read(key); err != nil {
			t.Fatal(err)
		}
	}
	old, err := secrets.New("old", map[string][]byte{"old": oldKey})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := secrets.New("new", map[string][]byte{"old": oldKey, "new": newKey})
	if err != nil {
		t.Fatal(err)
	}
	for _, keys := range []*secrets.Keyring{old, old, rotated, rotated} {
		if err := db.ConfigureTOTPEncryption(keys); err != nil {
			t.Fatal(err)
		}
		var stored string
		if err := db.Conn.QueryRow(`SELECT totp_secret FROM users WHERE id=$1`, uid).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored == seed || !keys.UsesActiveKey(stored) {
			t.Fatal("migration left plaintext or used the wrong key")
		}
		state, err := db.GetTOTPState(uid)
		if err != nil || state.Secret != seed || !state.Enabled || !state.Confirmed || state.LastStep != 21 {
			t.Fatal("migration changed enrolled factor or replay state")
		}
	}
	if err := db.ConsumeTOTPStep(uid, seed, 22); err != nil {
		t.Fatal(err)
	}
	if err := db.ConsumeTOTPStep(uid, seed, 22); !errors.Is(err, ErrTOTPReplay) {
		t.Fatal("rotation made a code reusable")
	}
}

func TestTOTPStartupMigrationIsAtomicOnMissingKey(t *testing.T) {
	db := newTestDB(t)
	first := mustUser(t, db, "legacy@example.com")
	second := mustUser(t, db, "missing-key@example.com")
	const seed = "JBSWY3DPEHPK3PXP"
	if _, err := db.Conn.Exec(`UPDATE users SET totp_secret=$1 WHERE id=$2`, seed, first); err != nil {
		t.Fatal(err)
	}
	if err := db.StageTOTPSecret(second, 0, seed); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfigureTOTPEncryption(testTOTPKeyring(t)); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatal("migration succeeded without the old key")
	}
	var stored string
	if err := db.Conn.QueryRow(`SELECT totp_secret FROM users WHERE id=$1`, first).Scan(&stored); err != nil || stored != seed {
		t.Fatal("failed migration partially committed")
	}
}

func TestTOTPStorageRejectsCrossAccountCopiesAndReplacedEnrolment(t *testing.T) {
	db := newTestDB(t)
	first := mustUser(t, db, "first@example.com")
	second := mustUser(t, db, "second@example.com")
	const seed = "JBSWY3DPEHPK3PXP"
	if err := db.StageTOTPSecret(first, 0, seed); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn.Exec(`UPDATE users SET totp_secret=(SELECT totp_secret FROM users WHERE id=$1) WHERE id=$2`, first, second); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetTOTPState(second); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatal("copied ciphertext was accepted for another account")
	}
	if err := db.StageTOTPSecret(first, 0, strings.Repeat("A", 32)); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfirmTOTP(first, 0, seed, 1, nil); !errors.Is(err, ErrTOTPStateChanged) {
		t.Fatal("replaced enrolment was confirmed")
	}
}

func TestTOTPCompatibilityRolloutReadsBothFormatsAndOnlyWritesEncrypted(t *testing.T) {
	db := newTestDB(t)
	keys := db.totpKeys
	first := mustUser(t, db, "compatible-legacy@example.com")
	second := mustUser(t, db, "compatible-new@example.com")
	const seed = "JBSWY3DPEHPK3PXP"
	if _, err := db.Conn.Exec(`UPDATE users SET totp_secret=$1 WHERE id=$2`, seed, first); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfigureTOTPCompatibility(keys); err != nil {
		t.Fatal(err)
	}
	if state, err := db.GetTOTPState(first); err != nil || state.Secret != seed {
		t.Fatal("compatibility mode cannot read legacy enrolment")
	}
	if err := db.StageTOTPSecret(second, 0, seed); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.Conn.QueryRow(`SELECT totp_secret FROM users WHERE id=$1`, second).Scan(&stored); err != nil || !keys.UsesActiveKey(stored) {
		t.Fatal("compatibility mode wrote plaintext")
	}
	if err := db.ConfigureTOTPEncryption(keys); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfigureTOTPCompatibility(keys); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{first, second} {
		if state, err := db.GetTOTPState(id); err != nil || state.Secret != seed {
			t.Fatal("compatibility rollback could not read the migrated records")
		}
	}
}
