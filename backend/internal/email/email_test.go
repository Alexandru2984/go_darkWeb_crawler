package email

import (
	"bufio"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// clearSMTP forces "dev mode" (no SMTP configured) so a valid recipient path
// returns nil without trying to reach a real mail server.
func clearSMTP(t *testing.T) {
	t.Helper()
	for _, k := range []string{"SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASS", "SMTP_FROM", "SMTP_TLS_MODE", "SMTP_TIMEOUT"} {
		t.Setenv(k, "")
	}
}

func configureSMTP(t *testing.T, host, port string) {
	t.Helper()
	clearSMTP(t)
	t.Setenv("SMTP_HOST", host)
	t.Setenv("SMTP_PORT", port)
	t.Setenv("SMTP_USER", "mailer@example.test")
	t.Setenv("SMTP_PASS", "test-password")
	t.Setenv("SMTP_FROM", "mailer@example.test")
	t.Setenv("SMTP_TLS_MODE", "starttls")
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

func TestSendMailRejectsPartialConfiguration(t *testing.T) {
	clearSMTP(t)
	t.Setenv("SMTP_USER", "configured-without-a-host")
	if err := SendVerificationEmail("valid.user@example.com", "tok-1234567890abcdef"); !errors.Is(err, ErrSMTPConfig) {
		t.Fatalf("partial SMTP configuration was silently accepted: %v", err)
	}
}

func TestSendMailRejectsInvalidSender(t *testing.T) {
	configureSMTP(t, "127.0.0.1", "587")
	t.Setenv("SMTP_FROM", "sender@example.com\r\nBcc: attacker@example.com")
	if err := SendVerificationEmail("valid.user@example.com", "tok-1234567890abcdef"); !errors.Is(err, ErrInvalidSender) {
		t.Fatalf("invalid SMTP sender was accepted: %v", err)
	}
}

func TestSendMailRefusesServerWithoutSTARTTLS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("220 test ESMTP\r\n"))
		reader := bufio.NewReader(conn)
		if _, readErr := reader.ReadString('\n'); readErr != nil {
			return
		}
		// Deliberately advertise AUTH but not STARTTLS. The client must stop
		// before authentication, so no credential or message crosses plaintext.
		_, _ = conn.Write([]byte("250-test\r\n250 AUTH PLAIN\r\n"))
	}()

	configureSMTP(t, host, port)
	t.Setenv("SMTP_TIMEOUT", "2s")
	err = SendVerificationEmail("valid.user@example.com", "tok-1234567890abcdef")
	if !errors.Is(err, ErrSMTPTLSRequired) {
		t.Fatalf("plaintext SMTP server was not rejected: %v", err)
	}
}

func TestSendMailHonorsOverallTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		time.Sleep(500 * time.Millisecond) // never send the SMTP greeting
	}()

	configureSMTP(t, host, port)
	t.Setenv("SMTP_TIMEOUT", "100ms")
	started := time.Now()
	err = SendVerificationEmail("valid.user@example.com", "tok-1234567890abcdef")
	if err == nil {
		t.Fatal("blackholed SMTP connection unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("SMTP timeout was not enforced promptly: %s", elapsed)
	}
}

func TestNormalizeBodyPreservesLinesWithoutHeaderAmbiguity(t *testing.T) {
	if got, want := normalizeBody("first\nsecond\r\nthird\rfourth"), "first\r\nsecond\r\nthird\r\nfourth"; got != want {
		t.Fatalf("normalizeBody = %q, want %q", got, want)
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
