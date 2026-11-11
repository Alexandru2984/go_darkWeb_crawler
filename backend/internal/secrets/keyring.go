// Package secrets encrypts stored TOTP seeds with keys held outside PostgreSQL.
package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrKeyring = errors.New("invalid or missing TOTP encryption keyring")
	ErrDecrypt = errors.New("TOTP secret authentication failed")
	keyID      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
)

// Keyring is immutable after construction. The active key seals new values;
// retained keys open older values, including encrypted database backups.
type Keyring struct {
	active string
	keys   map[string]cipher.AEAD
}

func New(active string, keys map[string][]byte) (*Keyring, error) {
	if !keyID.MatchString(active) || len(keys) < 1 || len(keys) > 16 {
		return nil, ErrKeyring
	}
	result := &Keyring{active: active, keys: make(map[string]cipher.AEAD, len(keys))}
	for id, key := range keys {
		if !keyID.MatchString(id) || len(key) != 32 {
			return nil, ErrKeyring
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, ErrKeyring
		}
		aead, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return nil, ErrKeyring
		}
		result.keys[id] = aead
	}
	if result.keys[active] == nil {
		return nil, ErrKeyring
	}
	return result, nil
}

// Load reads a bounded, private JSON file. Secret bytes never appear in errors.
func Load(path string) (*Keyring, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrKeyring
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o007 != 0 {
		return nil, ErrKeyring
	}
	if info.Size() > 65536 {
		return nil, ErrKeyring
	}
	return Decode(file)
}

// Decode is also used by the isolated restore verifier. Callers pass secrets
// through an anonymous pipe rather than command arguments or plaintext files.
func Decode(reader io.Reader) (*Keyring, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, ErrKeyring
	}
	var document struct {
		Version int               `json:"format_version"`
		Active  string            `json:"active"`
		Keys    map[string]string `json:"keys"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || document.Version != 1 {
		return nil, ErrKeyring
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, ErrKeyring
	}
	keys := make(map[string][]byte, len(document.Keys))
	for id, value := range document.Keys {
		key, err := base64.StdEncoding.Strict().DecodeString(value)
		if err != nil {
			return nil, ErrKeyring
		}
		keys[id] = key
	}
	return New(document.Active, keys)
}

func FromEnvironment() (*Keyring, error) {
	path := os.Getenv("TOTP_KEYRING_FILE")
	if path == "" {
		if directory := os.Getenv("CREDENTIALS_DIRECTORY"); directory != "" {
			path = filepath.Join(directory, "totp-keyring")
		}
	}
	if path == "" {
		return nil, ErrKeyring
	}
	return Load(path)
}

func (k *Keyring) prefix() string { return "totp:v1:" + k.active + ":" }

func (k *Keyring) UsesActiveKey(value string) bool {
	return strings.HasPrefix(value, k.prefix())
}

func (k *Keyring) Seal(userID int, secret string) (string, error) {
	if userID < 1 || secret == "" || len(secret) > 128 {
		return "", ErrDecrypt
	}
	prefix := k.prefix()
	// NewGCMWithRandomNonce generates and prepends a 96-bit random nonce.
	// Both the envelope header and account ID are authenticated to prevent
	// swapping ciphertexts between accounts or changing the key identifier.
	sealed := k.keys[k.active].Seal(nil, nil, []byte(secret), associatedData(userID, prefix))
	return prefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (k *Keyring) Open(userID int, value string) (string, error) {
	parts := strings.Split(value, ":")
	if userID < 1 || len(value) > 512 || len(parts) != 4 || parts[0] != "totp" || parts[1] != "v1" {
		return "", ErrDecrypt
	}
	aead := k.keys[parts[2]]
	if aead == nil {
		return "", ErrDecrypt
	}
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(parts[3])
	if err != nil {
		return "", ErrDecrypt
	}
	plain, err := aead.Open(nil, nil, sealed, associatedData(userID, "totp:v1:"+parts[2]+":"))
	if err != nil || len(plain) < 1 || len(plain) > 128 {
		return "", ErrDecrypt
	}
	return string(plain), nil
}

func associatedData(userID int, prefix string) []byte {
	return []byte("onion-spider:" + prefix + "account:" + strconv.Itoa(userID))
}
