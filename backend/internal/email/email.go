package email

import (
	"errors"
	"fmt"
	"log"
	"net/smtp"
	"os"
	"strings"
)

// ErrInvalidRecipient is returned when the recipient address is suspicious (CRLF injection).
var ErrInvalidRecipient = errors.New("invalid email address")

// SendVerificationEmail sends a confirmation link. Protects against:
//   - header injection: rejects any CR/LF in the address
//   - hardcoded URL: uses VERIFY_URL_BASE, falling back to CORS_ORIGIN
//
// If SMTP is not configured, logs locally (useful in dev) without exposing the token in prod stdout.
func SendVerificationEmail(to, token string) error {
	if strings.ContainsAny(to, "\r\n") || !strings.Contains(to, "@") || len(to) > 254 {
		return ErrInvalidRecipient
	}

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
		// Dev mode: do NOT log the token in plain text in prod — just say we would have sent it.
		log.Printf("[email] SMTP not configured — verification email NOT sent to %s", to)
		return nil
	}

	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)

	// From header = SMTP_FROM value; To = the validated recipient address above.
	msg := []byte(fmt.Sprintf(
		"From: %s\r\n"+
			"To: %s\r\n"+
			"Subject: Onion Spider account confirmation\r\n"+
			"MIME-Version: 1.0\r\n"+
			"Content-Type: text/plain; charset=UTF-8\r\n"+
			"\r\n"+
			"Hello,\r\n\r\nClick the link below to confirm your account:\r\n%s\r\n\r\n"+
			"This link expires in 24 hours.\r\n",
		from, to, verifyLink,
	))

	return smtp.SendMail(smtpHost+":"+smtpPort, auth, from, []string{to}, msg)
}
