package email

import (
	"errors"
	"fmt"
	"log"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
)

// ErrInvalidRecipient is returned when the recipient address fails RFC 5322
// parsing or fails our extra CRLF check.
var ErrInvalidRecipient = errors.New("invalid email address")

// SendVerificationEmail sends a confirmation link. Protects against:
//   - header injection: the recipient is parsed with net/mail.ParseAddress, so
//     only the canonical addr-spec form (no embedded CRLF, no display name
//     tricks) flows into the SMTP envelope and headers.
//   - hardcoded URL: uses VERIFY_URL_BASE, falling back to CORS_ORIGIN.
//
// If SMTP is not configured, logs locally (useful in dev) without exposing the
// token in prod stdout.
func SendVerificationEmail(to, token string) error {
	if len(to) > 254 {
		return ErrInvalidRecipient
	}
	// net/mail.ParseAddress is a recognized RFC 5322 sanitizer: it rejects
	// CRLF, header continuation tricks, and addresses without a localpart or
	// domain. We use the parsed `Address` field downstream so no untrusted
	// substring of the original input reaches the SMTP message.
	parsed, err := mail.ParseAddress(to)
	if err != nil || parsed.Address == "" {
		return ErrInvalidRecipient
	}
	if strings.ContainsAny(parsed.Address, "\r\n") {
		return ErrInvalidRecipient
	}
	cleanTo := parsed.Address

	smtpHost := os.Getenv("SMTP_HOST")
	smtpPort := os.Getenv("SMTP_PORT")
	smtpUser := os.Getenv("SMTP_USER")
	smtpPass := os.Getenv("SMTP_PASS")
	from := os.Getenv("SMTP_FROM")

	base := os.Getenv("VERIFY_URL_BASE")
	if base == "" {
		// Fallback: first origin from CORS_ORIGIN (useful with a single public origin).
		if co := os.Getenv("CORS_ORIGIN"); co != "" {
			base = strings.SplitN(co, ",", 2)[0]
		}
	}
	if base == "" {
		base = "http://localhost:8900"
	}
	verifyLink := fmt.Sprintf("%s/api/auth/verify?token=%s", strings.TrimRight(base, "/"), token)

	if smtpHost == "" || smtpUser == "" {
		// Dev mode: do NOT log the token in plain text in prod — just say we
		// would have sent it. %q sanitizes the recipient for log injection.
		log.Printf("[email] SMTP not configured — verification email NOT sent to %q", cleanTo)
		return nil
	}

	// Strip CR/LF from `from` too (env-supplied, but defense in depth: a
	// misconfigured SMTP_FROM with embedded headers would otherwise inject).
	cleanFrom := strings.ReplaceAll(strings.ReplaceAll(from, "\r", ""), "\n", "")

	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)

	msg := []byte(fmt.Sprintf(
		"From: %s\r\n"+
			"To: %s\r\n"+
			"Subject: Onion Spider account confirmation\r\n"+
			"MIME-Version: 1.0\r\n"+
			"Content-Type: text/plain; charset=UTF-8\r\n"+
			"\r\n"+
			"Hello,\r\n\r\nClick the link below to confirm your account:\r\n%s\r\n\r\n"+
			"This link expires in 24 hours.\r\n",
		cleanFrom, cleanTo, verifyLink,
	))

	return smtp.SendMail(smtpHost+":"+smtpPort, auth, cleanFrom, []string{cleanTo}, msg)
}
