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

// mockTorControlServer porneste un server TCP care simuleaza control port-ul Tor.
func mockTorControlServer(t *testing.T, authResponse, newnymResponse string) (addr string, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("nu pot porni mock server: %v", err)
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
		t.Fatalf("eroare neasteptata: %v", err)
	}
	if !renewed {
		t.Error("asteptat renewed=true, primit false")
	}
}

func TestRenewCircuit_AuthFailed(t *testing.T) {
	addr, cleanup := mockTorControlServer(t, "515 Authentication failed", "")
	defer cleanup()

	ctrl := NewTorController(addr, "badpassword", "", 0)
	renewed, err := ctrl.RenewCircuit()
	if err == nil {
		t.Fatal("asteptat eroare la autentificare esuata, primit nil")
	}
	if renewed {
		t.Error("asteptat renewed=false la eroare auth")
	}
	if !strings.Contains(err.Error(), "autentificare Tor esuata") {
		t.Errorf("mesaj de eroare neasteptat: %v", err)
	}
}

func TestRenewCircuit_NewnymFailed(t *testing.T) {
	addr, cleanup := mockTorControlServer(t, "250 OK", "552 Unrecognized signal")
	defer cleanup()

	ctrl := NewTorController(addr, "", "", 0)
	_, err := ctrl.RenewCircuit()
	if err == nil {
		t.Fatal("asteptat eroare la NEWNYM esuata")
	}
	if !strings.Contains(err.Error(), "NEWNYM esuata") {
		t.Errorf("mesaj de eroare neasteptat: %v", err)
	}
}

func TestRenewCircuit_Cooldown(t *testing.T) {
	addr, cleanup := mockTorControlServer(t, "250 OK", "250 OK")
	defer cleanup()

	ctrl := NewTorController(addr, "", "", 30*time.Second)

	renewed, err := ctrl.RenewCircuit()
	if err != nil {
		t.Fatalf("prima cerere esuata: %v", err)
	}
	if !renewed {
		t.Fatal("prima cerere: asteptat renewed=true")
	}

	// A doua cerere imediata — in cooldown, nu mai e server disponibil
	renewed, err = ctrl.RenewCircuit()
	if err != nil {
		t.Fatalf("a doua cerere a returnat eroare: %v", err)
	}
	if renewed {
		t.Error("a doua cerere in cooldown: asteptat renewed=false")
	}
}

func TestRenewCircuit_NoServer(t *testing.T) {
	ctrl := NewTorController("127.0.0.1:1", "", "", 0)
	_, err := ctrl.RenewCircuit()
	if err == nil {
		t.Fatal("asteptat eroare la conexiune refuzata")
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
		t.Fatalf("eroare la buildAuthCommand: %v", err)
	}
	if cmd != "AUTHENTICATE deadbeef" {
		t.Errorf("asteptat 'AUTHENTICATE deadbeef', primit '%s'", cmd)
	}
}

func TestBuildAuthCommand_Password(t *testing.T) {
	ctrl := &TorController{password: "supersecret"}
	cmd, err := ctrl.buildAuthCommand()
	if err != nil {
		t.Fatal(err)
	}
	if cmd != fmt.Sprintf("AUTHENTICATE %x", []byte("supersecret")) {
		t.Errorf("comanda incorecta: %s", cmd)
	}
}

func TestBuildAuthCommand_NoAuth(t *testing.T) {
	ctrl := &TorController{}
	cmd, err := ctrl.buildAuthCommand()
	if err != nil {
		t.Fatal(err)
	}
	if cmd != `AUTHENTICATE ""` {
		t.Errorf("comanda incorecta: %s", cmd)
	}
}
