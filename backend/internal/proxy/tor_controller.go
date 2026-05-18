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

// TorController connects to the Tor control port and can rotate the circuit.
type TorController struct {
	addr        string
	password    string // empty = try without password
	cookiePath  string // fallback: authenticate via cookie
	mu          sync.Mutex
	lastRenewal time.Time
	cooldown    time.Duration
}

// NewTorController creates a Tor controller.
// addr: "127.0.0.1:9051", password: "" (no-auth), cookiePath: "/run/tor/control.authcookie"
func NewTorController(addr, password, cookiePath string, cooldown time.Duration) *TorController {
	return &TorController{
		addr:       addr,
		password:   password,
		cookiePath: cookiePath,
		cooldown:   cooldown,
	}
}

// RenewCircuit sends SIGNAL NEWNYM to Tor — requests a new circuit for future connections.
// The call is thread-safe and respects the cooldown (Tor ignores NEWNYM more often than ~10s).
// Returns false if we are in the cooldown period.
func (c *TorController) RenewCircuit() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.lastRenewal) < c.cooldown {
		return false, nil // in cooldown period
	}

	conn, err := net.DialTimeout("tcp", c.addr, 5*time.Second)
	if err != nil {
		return false, fmt.Errorf("cannot connect to Tor control port %s: %w", c.addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	reader := bufio.NewReader(conn)

	// Authentication: try cookie, then password, then no-auth
	authCmd, err := c.buildAuthCommand()
	if err != nil {
		return false, fmt.Errorf("error building auth command: %w", err)
	}

	if _, err := fmt.Fprintf(conn, "%s\r\n", authCmd); err != nil {
		return false, fmt.Errorf("error sending auth command: %w", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("error reading auth response: %w", err)
	}
	if !strings.HasPrefix(line, "250") {
		return false, fmt.Errorf("tor authentication failed: %s", strings.TrimSpace(line))
	}

	// SIGNAL NEWNYM — request a new circuit
	if _, err := fmt.Fprintf(conn, "SIGNAL NEWNYM\r\n"); err != nil {
		return false, fmt.Errorf("error sending NEWNYM: %w", err)
	}
	line, err = reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("error reading NEWNYM response: %w", err)
	}
	if !strings.HasPrefix(line, "250") {
		return false, fmt.Errorf("NEWNYM failed: %s", strings.TrimSpace(line))
	}

	c.lastRenewal = time.Now()
	log.Println("🔄 Tor circuit renewed (SIGNAL NEWNYM).")
	return true, nil
}

// buildAuthCommand builds the appropriate authentication command:
// 1. Cookie (if file exists)
// 2. Password (if set)
// 3. No-auth AUTHENTICATE ""
func (c *TorController) buildAuthCommand() (string, error) {
	if c.cookiePath != "" {
		if data, err := os.ReadFile(c.cookiePath); err == nil {
			// Cookie = hex encoding of the binary file
			return fmt.Sprintf("AUTHENTICATE %x", data), nil
		}
	}
	if c.password != "" {
		return fmt.Sprintf("AUTHENTICATE %x", []byte(c.password)), nil
	}
	return `AUTHENTICATE ""`, nil
}
