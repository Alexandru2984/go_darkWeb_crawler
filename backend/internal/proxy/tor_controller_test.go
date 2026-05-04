package proxy

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// mockTorControlServer starts a TCP server that simulates the Tor control port.
func mockTorControlServer(t *testing.T, authResponse, newnymResponse string) (addr string, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot start mock server: %v", err)
	}

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))

		reader := bufio.NewReader(conn)

		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
		fmt.Fprintf(conn, "%s\r\n", authResponse)

		if !strings.HasPrefix(authResponse, "250") {
			return
		}

		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
		fmt.Fprintf(conn, "%s\r\n", newnymResponse)
	}()

	return ln.Addr().String(), func() { ln.Close() }
}

func TestRenewCircuit_Success(t *testing.T) {
	addr, cleanup := mockTorControlServer(t, "250 OK", "250 OK")
	defer cleanup()

	ctrl := NewTorController(addr, "", "", 0)
	renewed, err := ctrl.RenewCircuit()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !renewed {
		t.Error("expected renewed=true, got false")
	}
}

func TestRenewCircuit_AuthFailed(t *testing.T) {
	addr, cleanup := mockTorControlServer(t, "515 Authentication failed", "")
	defer cleanup()

	ctrl := NewTorController(addr, "badpassword", "", 0)
	renewed, err := ctrl.RenewCircuit()
	if err == nil {
		t.Fatal("expected error on failed authentication, got nil")
	}
	if renewed {
		t.Error("expected renewed=false on auth error")
	}
	if !strings.Contains(err.Error(), "Tor authentication failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRenewCircuit_NewnymFailed(t *testing.T) {
	addr, cleanup := mockTorControlServer(t, "250 OK", "552 Unrecognized signal")
	defer cleanup()

	ctrl := NewTorController(addr, "", "", 0)
	_, err := ctrl.RenewCircuit()
	if err == nil {
		t.Fatal("expected error on failed NEWNYM")
	}
	if !strings.Contains(err.Error(), "NEWNYM failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRenewCircuit_Cooldown(t *testing.T) {
	addr, cleanup := mockTorControlServer(t, "250 OK", "250 OK")
	defer cleanup()

	ctrl := NewTorController(addr, "", "", 30*time.Second)

	renewed, err := ctrl.RenewCircuit()
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	if !renewed {
		t.Fatal("first request: expected renewed=true")
	}

	// Second immediate request — in cooldown, no server available
	renewed, err = ctrl.RenewCircuit()
	if err != nil {
		t.Fatalf("second request returned error: %v", err)
	}
	if renewed {
		t.Error("second request in cooldown: expected renewed=false")
	}
}

func TestRenewCircuit_NoServer(t *testing.T) {
	ctrl := NewTorController("127.0.0.1:1", "", "", 0)
	_, err := ctrl.RenewCircuit()
	if err == nil {
		t.Fatal("expected error on refused connection")
	}
}

func TestBuildAuthCommand_Cookie(t *testing.T) {
	tmpFile := t.TempDir() + "/auth.cookie"
	if err := os.WriteFile(tmpFile, []byte{0xDE, 0xAD, 0xBE, 0xEF}, 0600); err != nil {
		t.Fatal(err)
	}

	ctrl := &TorController{cookiePath: tmpFile}
	cmd, err := ctrl.buildAuthCommand()
	if err != nil {
		t.Fatalf("error in buildAuthCommand: %v", err)
	}
	if cmd != "AUTHENTICATE deadbeef" {
		t.Errorf("expected 'AUTHENTICATE deadbeef', got '%s'", cmd)
	}
}

func TestBuildAuthCommand_Password(t *testing.T) {
	ctrl := &TorController{password: "supersecret"}
	cmd, err := ctrl.buildAuthCommand()
	if err != nil {
		t.Fatal(err)
	}
	if cmd != fmt.Sprintf("AUTHENTICATE %x", []byte("supersecret")) {
		t.Errorf("incorrect command: %s", cmd)
	}
}

func TestBuildAuthCommand_NoAuth(t *testing.T) {
	ctrl := &TorController{}
	cmd, err := ctrl.buildAuthCommand()
	if err != nil {
		t.Fatal(err)
	}
	if cmd != `AUTHENTICATE ""` {
		t.Errorf("incorrect command: %s", cmd)
	}
}
