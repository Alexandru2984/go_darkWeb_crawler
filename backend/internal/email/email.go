package email

import (
	"fmt"
	"net/smtp"
	"os"
)

func SendVerificationEmail(to, token string) error {
	smtpHost := os.Getenv("SMTP_HOST") // ex: smtp.gmail.com
	smtpPort := os.Getenv("SMTP_PORT") // ex: 587
	smtpUser := os.Getenv("SMTP_USER")
	smtpPass := os.Getenv("SMTP_PASS")
	from := os.Getenv("SMTP_FROM")

	verifyLink := fmt.Sprintf("https://go.micutu.com/api/auth/verify?token=%s", token)

	// Daca nu e configurat SMTP, doar printam in consola (pentru debug usor)
	if smtpHost == "" || smtpUser == "" {
		fmt.Printf("\n[EMAIL MOCK] Trimis catre: %s\nSubiect: Verifica contul Onion Spider\nLink: %s\n\n", to, verifyLink)
		return nil
	}

	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)
	
	msg := []byte(fmt.Sprintf("To: %s\r\n"+
		"Subject: Confirmare Cont Onion Spider\r\n"+
		"\r\n"+
		"Salut,\r\n\r\nApasa pe acest link pentru a-ti confirma contul:\r\n%s\r\n", to, verifyLink))

	return smtp.SendMail(smtpHost+":"+smtpPort, auth, from, []string{to}, msg)
}
