package proxy

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// TorController se conecteaza la Tor control port si poate rota circuitul.
type TorController struct {
	addr        string
	password    string // gol = incearca fara parola
	cookiePath  string // fallback: autentificare prin cookie
	mu          sync.Mutex
	lastRenewal time.Time
	cooldown    time.Duration
}

// NewTorController creeaza un controller Tor.
// addr: "127.0.0.1:9051", password: "" (no-auth), cookiePath: "/run/tor/control.authcookie"
func NewTorController(addr, password, cookiePath string, cooldown time.Duration) *TorController {
	return &TorController{
		addr:       addr,
		password:   password,
		cookiePath: cookiePath,
		cooldown:   cooldown,
	}
}

// RenewCircuit trimite SIGNAL NEWNYM la Tor — cere un nou circuit pentru conexiunile viitoare.
// Apelul e thread-safe si respecta cooldown-ul (Tor ignora NEWNYM mai des de ~10s).
// Returneaza false daca suntem in cooldown.
func (c *TorController) RenewCircuit() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.lastRenewal) < c.cooldown {
		return false, nil // in cooldown
	}

	conn, err := net.DialTimeout("tcp", c.addr, 5*time.Second)
	if err != nil {
		return false, fmt.Errorf("nu ma pot conecta la Tor control port %s: %w", c.addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	reader := bufio.NewReader(conn)

	// Autentificare: incearca cookie, altfel parola, altfel no-auth
	authCmd, err := c.buildAuthCommand()
	if err != nil {
		return false, fmt.Errorf("eroare la constructia comenzii auth: %w", err)
	}

	if _, err := fmt.Fprintf(conn, "%s\r\n", authCmd); err != nil {
		return false, fmt.Errorf("eroare la trimiterea auth: %w", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("eroare la citirea raspunsului auth: %w", err)
	}
	if !strings.HasPrefix(line, "250") {
		return false, fmt.Errorf("autentificare Tor esuata: %s", strings.TrimSpace(line))
	}

	// SIGNAL NEWNYM — cere circuit nou
	if _, err := fmt.Fprintf(conn, "SIGNAL NEWNYM\r\n"); err != nil {
		return false, fmt.Errorf("eroare la trimiterea NEWNYM: %w", err)
	}
	line, err = reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("eroare la citirea raspunsului NEWNYM: %w", err)
	}
	if !strings.HasPrefix(line, "250") {
		return false, fmt.Errorf("NEWNYM esuata: %s", strings.TrimSpace(line))
	}

	c.lastRenewal = time.Now()
	log.Println("🔄 Circuit Tor reinnoit (SIGNAL NEWNYM).")
	return true, nil
}

// buildAuthCommand construieste comanda de autentificare potrivita:
// 1. Cookie (daca fisierul exista)
// 2. Parola (daca e setata)
// 3. No-auth AUTHENTICATE ""
func (c *TorController) buildAuthCommand() (string, error) {
	if c.cookiePath != "" {
		if data, err := os.ReadFile(c.cookiePath); err == nil {
			// Cookie = hex encoding al fisierului binar
			return fmt.Sprintf("AUTHENTICATE %x", data), nil
		}
	}
	if c.password != "" {
		return fmt.Sprintf("AUTHENTICATE %x", []byte(c.password)), nil
	}
	return `AUTHENTICATE ""`, nil
}
