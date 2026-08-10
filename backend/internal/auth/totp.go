package auth

// RFC 6238 time-based one-time passwords, implemented here rather than pulled
// in as a dependency. The algorithm is eighty lines of HMAC and is verified
// below against the test vectors published in the RFC; a new module in the
// supply chain of an authentication path is the more expensive option.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// TOTPDigits and TOTPPeriod are the values every authenticator app assumes
	// by default. Changing them would silently break enrolment in most clients.
	TOTPDigits = 6
	TOTPPeriod = 30 * time.Second

	// totpSecretBytes is 160 bits, the size RFC 4226 recommends for HMAC-SHA1.
	totpSecretBytes = 20

	// TOTPSkew is how many periods either side of now are accepted. One step
	// tolerates ordinary clock drift and a code typed as it expires. Widening
	// it multiplies the codes valid at any instant, so it stays at one.
	TOTPSkew = 1
)

// ErrInvalidTOTPSecret marks a stored secret that cannot be decoded.
var ErrInvalidTOTPSecret = errors.New("invalid TOTP secret")

// GenerateTOTPSecret returns a new base32 secret in the unpadded form
// authenticator apps expect.
func GenerateTOTPSecret() string {
	b := make([]byte, totpSecretBytes)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}

// escapeOTPAuthLabel percent-encodes everything outside RFC 3986's unreserved
// set.
//
// url.PathEscape is not enough here: it leaves sub-delimiters such as "&" and
// "=" alone, because they are legal inside a path segment. A strict parser
// would still read them as part of the label, but authenticator apps are
// notorious for splitting these URIs on "?" and "&" by hand, and the account
// name is a user-chosen email address. Encoding the label down to unreserved
// characters removes the ambiguity instead of relying on every client to
// resolve it correctly.
func escapeOTPAuthLabel(s string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		unreserved := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~'
		if unreserved {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperhex[c>>4])
		b.WriteByte(upperhex[c&0x0f])
	}
	return b.String()
}

// TOTPProvisioningURI builds the otpauth:// URI an authenticator app consumes,
// usually via QR code. The account and issuer are escaped: an email address is
// user-controlled and must not be able to inject extra URI parameters.
func TOTPProvisioningURI(secret, account, issuer string) string {
	label := escapeOTPAuthLabel(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(TOTPDigits))
	q.Set("period", fmt.Sprint(int(TOTPPeriod.Seconds())))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// GenerateTOTPCode returns the code a correctly configured authenticator would
// display for secret at time at. It is the counterpart of ValidateTOTP and
// exists so callers can verify an enrolment end to end without reimplementing
// the algorithm — a second implementation would be free to share a bug with the
// first and prove nothing.
func GenerateTOTPCode(secret string, at time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	return totpCode(key, uint64(at.Unix())/uint64(TOTPPeriod.Seconds())), nil
}

// totpCode computes the code for one counter value.
func totpCode(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(buf[:])
	sum := mac.Sum(nil)

	// Dynamic truncation, RFC 4226 section 5.3.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	mod := uint32(1)
	for i := 0; i < TOTPDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", TOTPDigits, value%mod)
}

// decodeTOTPSecret accepts the secret in the shape users tend to paste it:
// spaces and lowercase are normal in authenticator UIs, and padding is
// optional.
func decodeTOTPSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	s = strings.TrimRight(s, "=")
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil || len(key) == 0 {
		return nil, ErrInvalidTOTPSecret
	}
	return key, nil
}

// ValidateTOTP reports whether code is valid for secret at time now, and
// returns the counter step it matched.
//
// The caller must persist that step and refuse anything less than or equal to
// it. Without that, a code stays replayable for its whole validity window —
// which is exactly the window an attacker who phished or shoulder-surfed one
// code is operating in.
func ValidateTOTP(secret, code string, now time.Time) (uint64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != TOTPDigits {
		return 0, false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return 0, false
	}

	counter := uint64(now.Unix()) / uint64(TOTPPeriod.Seconds())
	for delta := -TOTPSkew; delta <= TOTPSkew; delta++ {
		step := counter
		if delta < 0 {
			if step < uint64(-delta) {
				continue
			}
			step -= uint64(-delta)
		} else {
			step += uint64(delta)
		}
		// Constant-time: a byte-wise comparison would leak the expected code
		// one digit at a time to an attacker who can measure the response.
		if subtle.ConstantTimeCompare([]byte(totpCode(key, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// RecoveryCodeCount is how many single-use codes are issued at enrolment.
const RecoveryCodeCount = 10

// GenerateRecoveryCode returns one single-use code. The alphabet excludes
// characters that are easy to confuse when a code is copied off a screen under
// pressure, which is the only circumstance in which one is ever used.
func GenerateRecoveryCode() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	const length = 10
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	out := make([]byte, 0, length+1)
	for i, v := range b {
		if i == length/2 {
			out = append(out, '-')
		}
		out = append(out, alphabet[int(v)%len(alphabet)])
	}
	return string(out)
}

// NormalizeRecoveryCode makes lookup insensitive to how the user retyped it.
func NormalizeRecoveryCode(code string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
}
