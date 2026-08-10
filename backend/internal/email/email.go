package email

import (
	"errors"
	"log/slog"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
)

// ErrInvalidRecipient is returned when the recipient address fails RFC 5322
// parsing or fails our extra CRLF check.
var ErrInvalidRecipient = errors.New("invalid email address")

// verifyBaseURL returns the public base URL used in email links:
// VERIFY_URL_BASE, else the first CORS_ORIGIN, else localhost.
func verifyBaseURL() string {
	if base := os.Getenv("VERIFY_URL_BASE"); base != "" {
		return strings.TrimRight(base, "/")
	}
	if co := os.Getenv("CORS_ORIGIN"); co != "" {
		return strings.TrimRight(strings.SplitN(co, ",", 2)[0], "/")
	}
	return "http://localhost:8900"
}

// sendMail delivers a plain-text email, with defense against header injection:
//   - the recipient is parsed with net/mail.ParseAddress, so only a canonical
//     addr-spec (no embedded CRLF, no display-name tricks) reaches the SMTP
//     envelope and headers;
//   - subject/body are CRLF-controlled by the caller (we only interpolate a
//     server-generated link), and every header value is CRLF-stripped.
//
// If SMTP is not configured (dev), it logs the dropped recipient instead of
// sending — and never logs the token.
func sendMail(to, subject, bodyText string) error {
	if len(to) > 254 {
		return ErrInvalidRecipient
	}
	parsed, err := mail.ParseAddress(to)
	if err != nil || parsed.Address == "" {
		return ErrInvalidRecipient
	}
	if strings.ContainsAny(parsed.Address, "\r\n") {
		return ErrInvalidRecipient
	}
	cleanTo := stripCRLF(parsed.Address)

	smtpHost := os.Getenv("SMTP_HOST")
	smtpPort := os.Getenv("SMTP_PORT")
	smtpUser := os.Getenv("SMTP_USER")
	smtpPass := os.Getenv("SMTP_PASS")
	from := os.Getenv("SMTP_FROM")

	if smtpHost == "" || smtpUser == "" {
		// Dev mode: record only the dropped recipient (CRLF-stripped, JSON-escaped
		// by slog) — never the token/body.
		slog.Info("smtp_not_configured_email_dropped", "to", cleanTo)
		return nil
	}

	cleanFrom := stripCRLF(from)
	cleanSubject := stripCRLF(subject)
	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)

	// Build the message via Builder.WriteString of pre-sanitized values — keeps
	// the data-flow story simple for static analysis (no Sprintf over tainted
	// input). The body is server-generated text only.
	var b strings.Builder
	b.WriteString("From: ")
	b.WriteString(cleanFrom)
	b.WriteString("\r\nTo: ")
	b.WriteString(cleanTo)
	b.WriteString("\r\nSubject: ")
	b.WriteString(cleanSubject)
	b.WriteString("\r\nMIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(stripCRLF(bodyText))
	b.WriteString("\r\n")

	return smtp.SendMail(smtpHost+":"+smtpPort, auth, cleanFrom, []string{cleanTo}, []byte(b.String()))
}

func stripCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\n", "")
}

func verificationLink(token string) string {
	return verifyBaseURL() + "/verify-account#token=" + token
}

func passwordResetLink(token string) string {
	return verifyBaseURL() + "/reset-password#token=" + token
}

// SendVerificationEmail sends an account-confirmation link (valid 24h).
func SendVerificationEmail(to, token string) error {
	link := verificationLink(token)
	body := "Hello,\r\n\r\nClick the link below to confirm your account:\r\n" +
		stripCRLF(link) + "\r\n\r\nThis link expires in 24 hours."
	return sendMail(to, "Onion Spider account confirmation", body)
}

// SendPasswordResetEmail sends a password-reset link (valid 1h). The link points
// at the SPA reset page, which collects a new password and POSTs to
// /api/auth/reset with the token.
func SendPasswordResetEmail(to, token string) error {
	link := passwordResetLink(token)
	body := "Hello,\r\n\r\nWe received a request to reset your Onion Spider password.\r\n" +
		"Open the link below to choose a new password:\r\n" +
		stripCRLF(link) + "\r\n\r\nThis link expires in 1 hour. " +
		"If you did not request this, you can ignore this email."
	return sendMail(to, "Onion Spider password reset", body)
}
