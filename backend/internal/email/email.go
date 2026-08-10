package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidRecipient is returned when the recipient address fails RFC 5322
// parsing or fails our extra CRLF check.
var ErrInvalidRecipient = errors.New("invalid email address")

var (
	// ErrInvalidSender is returned when SMTP_FROM is not a valid mailbox.
	ErrInvalidSender = errors.New("invalid SMTP sender address")
	// ErrSMTPConfig identifies incomplete or unsafe SMTP configuration.
	ErrSMTPConfig = errors.New("invalid SMTP configuration")
	// ErrSMTPTLSRequired means the remote server did not offer the encrypted
	// transport required by the configured mode.
	ErrSMTPTLSRequired = errors.New("SMTP TLS is required")
)

const defaultSMTPTimeout = 10 * time.Second

type smtpConfig struct {
	host       string
	port       string
	user       string
	pass       string
	from       string
	tlsMode    string
	timeout    time.Duration
	serverName string
}

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
// If SMTP is entirely unconfigured (dev), it records only that a message was
// dropped. Partial configuration is an error: silently discarding production
// verification/reset mail would strand accounts.
func sendMail(to, subject, bodyText string) error {
	cleanTo, err := canonicalMailbox(to, ErrInvalidRecipient)
	if err != nil {
		return err
	}
	cfg, err := smtpConfigFromEnv()
	if err != nil {
		return err
	}
	if cfg == nil {
		// The privacy logger pseudonymizes this value before it reaches journald.
		slog.Info("smtp_not_configured_email_dropped", "to", cleanTo)
		return nil
	}

	cleanSubject := stripCRLF(subject)

	// Build the message via Builder.WriteString of pre-sanitized values — keeps
	// the data-flow story simple for static analysis (no Sprintf over tainted
	// input). The body is server-generated text only.
	var b strings.Builder
	b.WriteString("From: ")
	b.WriteString(cfg.from)
	b.WriteString("\r\nTo: ")
	b.WriteString(cleanTo)
	b.WriteString("\r\nSubject: ")
	b.WriteString(cleanSubject)
	b.WriteString("\r\nMIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(normalizeBody(bodyText))
	b.WriteString("\r\n")

	return deliverSMTP(cfg, cleanTo, []byte(b.String()))
}

func canonicalMailbox(raw string, sentinel error) (string, error) {
	if len(raw) == 0 || len(raw) > 254 || strings.ContainsAny(raw, "\r\n\x00") {
		return "", sentinel
	}
	parsed, err := mail.ParseAddress(raw)
	if err != nil || parsed.Address == "" || len(parsed.Address) > 254 || strings.ContainsAny(parsed.Address, "\r\n\x00") {
		return "", sentinel
	}
	return parsed.Address, nil
}

// smtpConfigFromEnv returns nil only when SMTP is deliberately disabled. TLS
// is mandatory whenever delivery is enabled: port 465 defaults to implicit
// TLS; every other port defaults to STARTTLS.
func smtpConfigFromEnv() (*smtpConfig, error) {
	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	port := strings.TrimSpace(os.Getenv("SMTP_PORT"))
	user := strings.TrimSpace(os.Getenv("SMTP_USER"))
	pass := os.Getenv("SMTP_PASS")
	from := strings.TrimSpace(os.Getenv("SMTP_FROM"))

	if host == "" {
		if user != "" || pass != "" || from != "" {
			return nil, fmt.Errorf("%w: SMTP_HOST is required when credentials or sender are set", ErrSMTPConfig)
		}
		return nil, nil
	}
	if user == "" || pass == "" || from == "" {
		return nil, fmt.Errorf("%w: SMTP_USER, SMTP_PASS and SMTP_FROM are required", ErrSMTPConfig)
	}
	if strings.ContainsAny(host, "\r\n\x00/ ") || strings.ContainsAny(user, "\r\n\x00") {
		return nil, fmt.Errorf("%w: invalid host or username", ErrSMTPConfig)
	}
	if port == "" {
		port = "587"
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, fmt.Errorf("%w: invalid SMTP_PORT", ErrSMTPConfig)
	}
	cleanFrom, err := canonicalMailbox(from, ErrInvalidSender)
	if err != nil {
		return nil, err
	}

	tlsMode := strings.ToLower(strings.TrimSpace(os.Getenv("SMTP_TLS_MODE")))
	if tlsMode == "" {
		if portNumber == 465 {
			tlsMode = "implicit"
		} else {
			tlsMode = "starttls"
		}
	}
	if tlsMode != "starttls" && tlsMode != "implicit" {
		return nil, fmt.Errorf("%w: SMTP_TLS_MODE must be starttls or implicit", ErrSMTPConfig)
	}

	timeout := defaultSMTPTimeout
	if rawTimeout := strings.TrimSpace(os.Getenv("SMTP_TIMEOUT")); rawTimeout != "" {
		timeout, err = time.ParseDuration(rawTimeout)
		if err != nil || timeout < 100*time.Millisecond || timeout > 2*time.Minute {
			return nil, fmt.Errorf("%w: SMTP_TIMEOUT must be between 100ms and 2m", ErrSMTPConfig)
		}
	}

	return &smtpConfig{
		host:       host,
		port:       port,
		user:       user,
		pass:       pass,
		from:       cleanFrom,
		tlsMode:    tlsMode,
		timeout:    timeout,
		serverName: host,
	}, nil
}

func deliverSMTP(cfg *smtpConfig, recipient string, message []byte) error {
	address := net.JoinHostPort(cfg.host, cfg.port)
	deadline := time.Now().Add(cfg.timeout)
	dialer := &net.Dialer{Timeout: cfg.timeout}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: cfg.serverName,
	}

	var conn net.Conn
	var err error
	if cfg.tlsMode == "implicit" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
	} else {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("SMTP connect: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("SMTP deadline: %w", err)
	}

	client, err := smtp.NewClient(conn, cfg.host)
	if err != nil {
		return fmt.Errorf("SMTP greeting: %w", err)
	}
	defer client.Close()

	if cfg.tlsMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("%w: server does not advertise STARTTLS", ErrSMTPTLSRequired)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("SMTP STARTTLS: %w", err)
		}
	}

	auth := smtp.PlainAuth("", cfg.user, cfg.pass, cfg.host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP authentication: %w", err)
	}
	if err := client.Mail(cfg.from); err != nil {
		return fmt.Errorf("SMTP sender: %w", err)
	}
	if err := client.Rcpt(recipient); err != nil {
		return fmt.Errorf("SMTP recipient: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return fmt.Errorf("SMTP body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("SMTP body close: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("SMTP quit: %w", err)
	}
	return nil
}

func stripCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\n", "")
}

func normalizeBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
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
