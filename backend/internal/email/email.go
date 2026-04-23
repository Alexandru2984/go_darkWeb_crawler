package email

import (
	"errors"
	"fmt"
	"log"
	"net/smtp"
	"os"
	"strings"
)

// ErrInvalidRecipient se returneaza cand adresa destinatar e suspecta (CRLF injection).
var ErrInvalidRecipient = errors.New("adresa email invalida")

// SendVerificationEmail trimite un link de confirmare. Protejeaza impotriva:
//   - header injection: refuza orice CR/LF in adresa
//   - URL hardcodat: foloseste VERIFY_URL_BASE, fallback pe CORS_ORIGIN
//
// Daca SMTP nu e configurat, logheaza local (util in dev) fara sa expuna tokenul in stdout-ul prod.
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
		// Fallback: prima origine din CORS_ORIGIN (util daca ai o singura origine publica).
		if co := os.Getenv("CORS_ORIGIN"); co != "" {
			base = strings.SplitN(co, ",", 2)[0]
		}
	}
	if base == "" {
		base = "http://localhost:8900"
	}
	verifyLink := fmt.Sprintf("%s/api/auth/verify?token=%s", strings.TrimRight(base, "/"), token)

	if smtpHost == "" || smtpUser == "" {
		// Dev mode: NU logam tokenul in clar in prod — doar spunem ca am fi trimis.
		log.Printf("[email] SMTP neconfigurat — email de verificare NEtrimis catre %s", to)
		return nil
	}

	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)

	// From header = valoarea SMTP_FROM; To = destinatarul validat mai sus.
	msg := []byte(fmt.Sprintf(
		"From: %s\r\n"+
			"To: %s\r\n"+
			"Subject: Confirmare cont Onion Spider\r\n"+
			"MIME-Version: 1.0\r\n"+
			"Content-Type: text/plain; charset=UTF-8\r\n"+
			"\r\n"+
			"Salut,\r\n\r\nApasa pe acest link pentru a-ti confirma contul:\r\n%s\r\n\r\n"+
			"Linkul expira in 24 de ore.\r\n",
		from, to, verifyLink,
	))

	return smtp.SendMail(smtpHost+":"+smtpPort, auth, from, []string{to}, msg)
}
