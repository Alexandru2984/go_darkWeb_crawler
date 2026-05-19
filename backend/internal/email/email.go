package email

import (
	"errors"
	"fmt"
	"log/slog"
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
		// Dev mode: do NOT log the token in plain text — just record the
		// dropped recipient. cleanTo is already CRLF-stripped above; slog
		// serializes the value as a JSON-escaped attribute so log injection
		// is structurally impossible.
		slog.Info("smtp_not_configured_email_dropped", "to", cleanTo)
		return nil
	}

	// Strip CR/LF from `from` too — inline strings.ReplaceAll for the same
	// CodeQL reason as cleanTo above.
	cleanFrom := strings.ReplaceAll(from, "\r", "")
	cleanFrom = strings.ReplaceAll(cleanFrom, "\n", "")

	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)

	// Build msg with strings.Builder + explicit WriteString — no fmt.Sprintf.
	// CodeQL's go/email-injection appears to mark a Sprintf result as tainted
	// even when every interpolated arg is individually sanitized, because the
	// query tracks taint conservatively across format-string interpolation.
	// Concatenating via Builder.WriteString of pre-sanitized values keeps
	// the data-flow story simple: each WriteString receives a value whose
	// only producer is a recognized strings.ReplaceAll sanitizer.
	var b strings.Builder
	b.WriteString("From: ")
	b.WriteString(cleanFrom)
	b.WriteString("\r\nTo: ")
	b.WriteString(cleanTo)
	b.WriteString("\r\nSubject: Onion Spider account confirmation\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString("Hello,\r\n\r\nClick the link below to confirm your account:\r\n")
	b.WriteString(verifyLink)
	b.WriteString("\r\n\r\nThis link expires in 24 hours.\r\n")

	return smtp.SendMail(smtpHost+":"+smtpPort, auth, cleanFrom, []string{cleanTo}, []byte(b.String()))
}
