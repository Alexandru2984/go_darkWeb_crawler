package secrets

import (
	"bytes"
	"encoding/base32"
	"encoding/json"
	"io"
	"strings"
)

// CheckBackup authenticates restored envelopes using only the downloaded
// encrypted keyring snapshot. It never reads the host's current keyring or DB.
func CheckBackup(reader io.Reader) error {
	const limit = 32 * 1024 * 1024
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || len(raw) > limit {
		return ErrDecrypt
	}
	var document struct {
		Keyring json.RawMessage `json:"keyring"`
		Seeds   []struct {
			UserID int    `json:"user_id"`
			Value  string `json:"value"`
		} `json:"seeds"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || decoder.Decode(new(any)) != io.EOF || document.Seeds == nil {
		return ErrDecrypt
	}
	keys, err := Decode(bytes.NewReader(document.Keyring))
	if err != nil {
		return err
	}
	for _, seed := range document.Seeds {
		if strings.HasPrefix(seed.Value, "totp:") {
			if _, err := keys.Open(seed.UserID, seed.Value); err != nil {
				return err
			}
		} else {
			decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(seed.Value)
			if seed.UserID < 1 || len(seed.Value) > 128 || err != nil || len(decoded) < 10 {
				return ErrDecrypt
			}
		}
	}
	return nil
}
