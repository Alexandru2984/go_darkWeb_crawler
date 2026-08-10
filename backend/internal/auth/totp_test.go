package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// rfc6238Secret is the 20-byte ASCII seed "12345678901234567890" from RFC 6238
// Appendix B, expressed in the base32 form this package stores.
func rfc6238Secret(t *testing.T) string {
	t.Helper()
	return base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString([]byte("12345678901234567890"))
}

// TestValidateTOTPMatchesRFC6238Vectors checks the implementation against the
// published SHA-1 test vectors. Hand-rolled crypto that is only tested against
// itself proves nothing.
func TestValidateTOTPMatchesRFC6238Vectors(t *testing.T) {
	secret := rfc6238Secret(t)
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		t.Fatalf("decode seed: %v", err)
	}

	// Unix time -> expected 8-digit code from the RFC, truncated to our 6.
	vectors := []struct {
		unix int64
		code string // last TOTPDigits digits of the RFC's 8-digit value
	}{
		{59, "287082"},          // RFC: 94287082
		{1111111109, "081804"},  // RFC: 07081804
		{1111111111, "050471"},  // RFC: 14050471
		{1234567890, "005924"},  // RFC: 89005924
		{2000000000, "279037"},  // RFC: 69279037
		{20000000000, "353130"}, // RFC: 65353130
	}

	for _, v := range vectors {
		counter := uint64(v.unix) / uint64(TOTPPeriod.Seconds())
		if got := totpCode(key, counter); got != v.code {
			t.Errorf("totpCode at t=%d: got %s, want %s", v.unix, got, v.code)
		}
		if _, ok := ValidateTOTP(secret, v.code, time.Unix(v.unix, 0)); !ok {
			t.Errorf("ValidateTOTP rejected the RFC code %s at t=%d", v.code, v.unix)
		}
	}
}

func TestValidateTOTPAcceptsOneStepOfDrift(t *testing.T) {
	secret := rfc6238Secret(t)
	key, _ := decodeTOTPSecret(secret)
	now := time.Unix(1111111109, 0)
	counter := uint64(now.Unix()) / uint64(TOTPPeriod.Seconds())

	previous := totpCode(key, counter-1)
	next := totpCode(key, counter+1)
	tooOld := totpCode(key, counter-2)

	if _, ok := ValidateTOTP(secret, previous, now); !ok {
		t.Error("a code from the previous step should be accepted (clock drift)")
	}
	if _, ok := ValidateTOTP(secret, next, now); !ok {
		t.Error("a code from the next step should be accepted (clock drift)")
	}
	if _, ok := ValidateTOTP(secret, tooOld, now); ok {
		t.Error("a code two steps old should be rejected")
	}
}

func TestValidateTOTPReturnsTheMatchedStepForReplayDefense(t *testing.T) {
	// The caller stores this step and refuses anything at or below it. Without
	// the step there is no way to tell a fresh code from a replayed one.
	secret := rfc6238Secret(t)
	key, _ := decodeTOTPSecret(secret)
	now := time.Unix(1234567890, 0)
	counter := uint64(now.Unix()) / uint64(TOTPPeriod.Seconds())

	step, ok := ValidateTOTP(secret, totpCode(key, counter), now)
	if !ok {
		t.Fatal("current code should validate")
	}
	if step != counter {
		t.Fatalf("matched step = %d, want %d", step, counter)
	}

	earlier, ok := ValidateTOTP(secret, totpCode(key, counter-1), now)
	if !ok {
		t.Fatal("previous code should validate within skew")
	}
	if earlier >= step {
		t.Fatalf("an older code reported step %d, not below %d — replay would be undetectable", earlier, step)
	}
}

func TestValidateTOTPRejectsMalformedInput(t *testing.T) {
	secret := rfc6238Secret(t)
	for _, code := range []string{"", "12345", "1234567", "abcdef", "12 456", "١٢٣٤٥٦"} {
		if _, ok := ValidateTOTP(secret, code, time.Unix(59, 0)); ok {
			t.Errorf("malformed code %q was accepted", code)
		}
	}
	if _, ok := ValidateTOTP("not-base32-!!!", "287082", time.Unix(59, 0)); ok {
		t.Error("an undecodable secret must not validate anything")
	}
}

func TestDecodeTOTPSecretAcceptsHowUsersPasteIt(t *testing.T) {
	secret := rfc6238Secret(t)
	want, err := decodeTOTPSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	// Authenticator UIs show secrets grouped in lowercase blocks, and some add
	// padding. All three must decode to the same key.
	for _, variant := range []string{
		strings.ToLower(secret),
		secret[:4] + " " + secret[4:8] + " " + secret[8:],
		secret + "======",
	} {
		got, err := decodeTOTPSecret(variant)
		if err != nil {
			t.Fatalf("variant %q failed to decode: %v", variant, err)
		}
		if string(got) != string(want) {
			t.Fatalf("variant %q decoded to a different key", variant)
		}
	}
}

func TestGenerateTOTPSecretIsUsableAndUnique(t *testing.T) {
	a, b := GenerateTOTPSecret(), GenerateTOTPSecret()
	if a == b {
		t.Fatal("two generated secrets were identical")
	}
	key, err := decodeTOTPSecret(a)
	if err != nil {
		t.Fatalf("generated secret does not decode: %v", err)
	}
	if len(key) != totpSecretBytes {
		t.Fatalf("secret is %d bytes, want %d", len(key), totpSecretBytes)
	}
	// A freshly generated secret must validate its own current code.
	now := time.Now()
	code := totpCode(key, uint64(now.Unix())/uint64(TOTPPeriod.Seconds()))
	if _, ok := ValidateTOTP(a, code, now); !ok {
		t.Fatal("a generated secret failed to validate its own code")
	}
}

func TestProvisioningURIEscapesUserControlledFields(t *testing.T) {
	// The account is an email address the user chose. It must not be able to
	// terminate the label and append its own parameters.
	uri := TOTPProvisioningURI("JBSWY3DPEHPK3PXP", "a?b&issuer=evil@example.com", "Onion Spider")
	if strings.Contains(uri, "issuer=evil") {
		t.Fatalf("account field injected a parameter: %s", uri)
	}
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("unexpected URI shape: %s", uri)
	}
	if !strings.Contains(uri, "secret=JBSWY3DPEHPK3PXP") {
		t.Fatalf("secret missing from URI: %s", uri)
	}
}

func TestRecoveryCodesAreDistinctAndNormalizable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c := GenerateRecoveryCode()
		if seen[c] {
			t.Fatalf("duplicate recovery code generated: %s", c)
		}
		seen[c] = true
		if NormalizeRecoveryCode(strings.ToUpper(" "+c+" ")) != c {
			t.Fatalf("normalization did not round-trip %q", c)
		}
	}
}
