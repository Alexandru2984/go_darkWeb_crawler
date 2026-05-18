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

// logScrub strips CR/LF before logging user-supplied values; see the same
// helper in the api package for the explanation.
func logScrub(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

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
	// Inline strings.ReplaceAll (NOT via logScrub) for the values that flow
	// into smtp.SendMail. CodeQL's go/email-injection query has a stricter
	// sanitizer model than go/log-injection and does not always propagate
	// the clean taint state through a user-defined wrapper; using the
	// standard-library call directly at the assignment site is unambiguous.
	cleanTo := strings.ReplaceAll(parsed.Address, "\r", "")
	cleanTo = strings.ReplaceAll(cleanTo, "\n", "")

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
	verifyLink = strings.ReplaceAll(verifyLink, "\r", "")
	verifyLink = strings.ReplaceAll(verifyLink, "\n", "")

	if smtpHost == "" || smtpUser == "" {
		// Dev mode: do NOT log the token in plain text in prod — just say we
		// would have sent it. cleanTo is already CRLF-stripped above; pass it
		// through logScrub again so the data flow visibly stays sanitized.
		log.Printf("[email] SMTP not configured — verification email NOT sent to %s", logScrub(cleanTo))
		return nil
	}

	// Strip CR/LF from `from` too — inline strings.ReplaceAll for the same
	// CodeQL reason as cleanTo above.
	cleanFrom := strings.ReplaceAll(from, "\r", "")
	cleanFrom = strings.ReplaceAll(cleanFrom, "\n", "")

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
