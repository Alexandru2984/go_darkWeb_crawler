package email

import (
	"errors"
	"strings"
	"testing"
)

// clearSMTP forces "dev mode" (no SMTP configured) so a valid recipient path
// returns nil without trying to reach a real mail server.
func clearSMTP(t *testing.T) {
	t.Helper()
	for _, k := range []string{"SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASS", "SMTP_FROM"} {
		t.Setenv(k, "")
	}
}

func TestSendVerificationEmail_RejectsHeaderInjection(t *testing.T) {
	clearSMTP(t)
	// Classic SMTP header-injection payloads: a CRLF in the recipient would let
	// an attacker add Bcc/extra headers. mail.ParseAddress must reject these.
	payloads := []string{
		"victim@example.com\r\nBcc: attacker@evil.com",
		"victim@example.com\nCc: attacker@evil.com",
		"victim@example.com%0d%0aBcc:x@y.com", // pre-decoded form
		"a@b.com\r\nSubject: spam",
	}
	for _, p := range payloads {
		if err := SendVerificationEmail(p, "tok-1234567890abcdef"); !errors.Is(err, ErrInvalidRecipient) {
			t.Errorf("header-injection payload accepted (err=%v): %q", err, p)
		}
	}
}

func TestSendVerificationEmail_RejectsMalformed(t *testing.T) {
	clearSMTP(t)
	for _, addr := range []string{"", "not-an-email", "@no-local.com", "no-domain@", "spaces in@addr.com"} {
		if err := SendVerificationEmail(addr, "tok-1234567890abcdef"); !errors.Is(err, ErrInvalidRecipient) {
			t.Errorf("malformed address accepted (err=%v): %q", err, addr)
		}
	}
}

func TestSendVerificationEmail_RejectsOverlongAddress(t *testing.T) {
	clearSMTP(t)
	addr := strings.Repeat("a", 250) + "@x.com" // > 254 chars total
	if err := SendVerificationEmail(addr, "tok-1234567890abcdef"); !errors.Is(err, ErrInvalidRecipient) {
		t.Errorf("overlong address accepted: err=%v", err)
	}
}

func TestSendVerificationEmail_ValidDevModeNoError(t *testing.T) {
	clearSMTP(t)
	// With SMTP unconfigured and a valid recipient, the function drops the mail
	// (dev mode) and returns nil rather than erroring.
	if err := SendVerificationEmail("valid.user@example.com", "tok-1234567890abcdef"); err != nil {
		t.Errorf("valid recipient in dev mode should not error, got: %v", err)
	}
}

func TestAccountLinksKeepCredentialsOutOfHTTPRequests(t *testing.T) {
	t.Setenv("VERIFY_URL_BASE", "https://example.test/")
	const token = "0123456789abcdef0123456789abcdef"

	for name, link := range map[string]string{
		"verification": verificationLink(token),
		"reset":        passwordResetLink(token),
	} {
		if strings.Contains(link, "?token=") {
			t.Fatalf("%s link exposed the credential in a query string: %s", name, link)
		}
		if !strings.HasSuffix(link, "#token="+token) {
			t.Fatalf("%s link does not keep the credential in the URL fragment: %s", name, link)
		}
	}
}
