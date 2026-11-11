package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestEnvelopesAuthenticateAccountHeaderAndCiphertext(t *testing.T) {
	keys, err := New("current", map[string][]byte{"current": testKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	const seed = "JBSWY3DPEHPK3PXP"
	a, err := keys.Seal(7, seed)
	if err != nil {
		t.Fatal(err)
	}
	b, err := keys.Seal(7, seed)
	if err != nil || a == b || strings.Contains(a, seed) {
		t.Fatal("encryption reused a nonce or retained plaintext")
	}
	if plain, err := keys.Open(7, a); err != nil || plain != seed {
		t.Fatal("authenticated round trip failed")
	}
	parts := strings.Split(a, ":")
	payload, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		t.Fatal(err)
	}
	payload[0] ^= 1
	tampered := strings.Join(parts[:3], ":") + ":" + base64.RawURLEncoding.EncodeToString(payload)
	for _, value := range []string{tampered, strings.Replace(a, ":current:", ":unknown:", 1), strings.Replace(a, ":v1:", ":v2:", 1), seed, "totp:v1:current:AA"} {
		if _, err := keys.Open(7, value); !errors.Is(err, ErrDecrypt) {
			t.Fatal("altered envelope or plaintext was accepted")
		}
	}
	if _, err := keys.Open(8, a); !errors.Is(err, ErrDecrypt) {
		t.Fatal("ciphertext copied between accounts was accepted")
	}
}

func TestKeyRotationRetainsReadAccessAndUsesNewKey(t *testing.T) {
	oldKey, newKey := testKey(t), testKey(t)
	old, err := New("old", map[string][]byte{"old": oldKey})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := old.Seal(1, "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := New("new", map[string][]byte{"old": oldKey, "new": newKey})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := rotated.Open(1, sealed)
	if err != nil || rotated.UsesActiveKey(sealed) {
		t.Fatal("retained key cannot read the old envelope")
	}
	updated, err := rotated.Seal(1, plain)
	if err != nil || !rotated.UsesActiveKey(updated) {
		t.Fatal("rotation did not select the active key")
	}
	if _, err := old.Open(1, updated); !errors.Is(err, ErrDecrypt) {
		t.Fatal("keyring without the new key accepted its envelope")
	}
}

func TestKeyringRejectsInvalidOrPublicConfiguration(t *testing.T) {
	for _, active := range []string{"", "missing", "bad:id"} {
		if _, err := New(active, map[string][]byte{"key": testKey(t)}); !errors.Is(err, ErrKeyring) {
			t.Fatal("invalid active key accepted")
		}
	}
	if _, err := New("key", map[string][]byte{"key": bytes.Repeat([]byte{1}, 16)}); err == nil {
		t.Fatal("non-256-bit key accepted")
	}
	path := filepath.Join(t.TempDir(), "keyring.json")
	document := map[string]any{"format_version": 1, "active": "key", "keys": map[string]string{"key": base64.StdEncoding.EncodeToString(testKey(t))}}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); !errors.Is(err, ErrKeyring) {
		t.Fatal("world-readable key file accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{append(raw, []byte("{}")...), []byte(`{"format_version":2}`), bytes.Repeat([]byte("x"), 65537)} {
		if err := os.WriteFile(path, invalid, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); !errors.Is(err, ErrKeyring) {
			t.Fatal("invalid key document accepted")
		}
	}
}

func TestRestoreVerifierRequiresMatchingKeysAndAccountBindings(t *testing.T) {
	key := testKey(t)
	keys, err := New("snapshot", map[string][]byte{"snapshot": key})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := keys.Seal(4, "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	document := map[string]any{
		"keyring": map[string]any{"format_version": 1, "active": "snapshot",
			"keys": map[string]string{"snapshot": base64.StdEncoding.EncodeToString(key)}},
		"seeds": []map[string]any{{"user_id": 4, "value": sealed}},
	}
	check := func() error {
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		return CheckBackup(bytes.NewReader(raw))
	}
	if err := check(); err != nil {
		t.Fatal(err)
	}
	document["seeds"] = []map[string]any{{"user_id": 5, "value": sealed}}
	if err := check(); !errors.Is(err, ErrDecrypt) {
		t.Fatal("restored seed accepted under the wrong account ID")
	}
	document["seeds"] = []map[string]any{{"user_id": 4, "value": "JBSWY3DPEHPK3PXP"}}
	if err := check(); err != nil {
		t.Fatal("pre-migration archive could not be checked")
	}
	delete(document, "seeds")
	if err := check(); !errors.Is(err, ErrDecrypt) {
		t.Fatal("missing restored records were accepted")
	}
}
