package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"onion-spider/internal/auth"
)

func TestAdminInvitationsRequireLiveAdminAndMFA(t *testing.T) {
	deliveries := 0
	h, db := newAPI(t, func(cfg *Config) {
		cfg.RequireAdminMFA = true
		cfg.SendInvitation = func(_, _ string) error { deliveries++; return nil }
	})
	_, user := mkUser(t, db, "user@example.com", "user")
	id, admin := mkUser(t, db, "admin@example.com", "admin")
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {user, 403}, {admin, 403}} {
		for _, endpoint := range []struct{ method, path string }{
			{"GET", "/api/admin/users"}, {"POST", "/api/admin/users"}, {"POST", "/api/admin/users/2/invitation"},
		} {
			rr := doWithBody(t, h, endpoint.method, endpoint.path, tc.token, strings.NewReader(`{"email":"new@example.com","password":"anything"}`))
			if rr.Code != tc.status {
				t.Fatalf("%s %s: got %d want %d", endpoint.method, endpoint.path, rr.Code, tc.status)
			}
		}
	}
	if _, err := db.Conn.Exec(`UPDATE users SET role='user' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if rr := do(t, h, "GET", "/api/admin/users", admin); rr.Code != 403 {
		t.Fatalf("stale admin role accepted: %d", rr.Code)
	}
	if deliveries != 0 {
		t.Fatal("an unauthorized request sent mail")
	}
}

func TestClosedRegistrationAdminInvitationAndActivation(t *testing.T) {
	var recipient, activation string
	h, db := newAPI(t, func(cfg *Config) {
		cfg.RequireAdminMFA = true
		cfg.SendInvitation = func(to, token string) error { recipient, activation = to, token; return nil }
	})
	const ownerPassword = "Owner-Passphrase-9!"
	_, session := mkVerifiedUser(t, db, "owner@example.com", ownerPassword, "admin")
	_, recovery := enrolTOTP(t, h, session, ownerPassword)
	session = loginTOTPToken(t, h, "owner@example.com", ownerPassword, recovery[0])
	withoutMFA := `{"email":"missing-mfa@example.com","password":"` + ownerPassword + `"}`
	if rr := doWithBody(t, h, "POST", "/api/admin/users", session, strings.NewReader(withoutMFA)); rr.Code != 403 {
		t.Fatal("administrator password without second factor accepted")
	}
	if rr := doWithBody(t, h, "POST", "/api/auth/register", "", strings.NewReader(`{"email":"public@example.com","password":"Public-Password-9!"}`)); rr.Code != 403 {
		t.Fatal("public registration unexpectedly opened")
	}
	invite, _ := json.Marshal(map[string]string{"email": " New@Example.com ", "password": ownerPassword, "code": recovery[1]})
	rr := doWithBody(t, h, "POST", "/api/admin/users", session, strings.NewReader(string(invite)))
	if rr.Code != 201 {
		t.Fatalf("invite: %d %s", rr.Code, rr.Body.String())
	}
	if recipient != "new@example.com" || len(activation) != 64 {
		t.Fatal("invitation was not delivered to the normalized recipient")
	}
	if strings.Contains(rr.Body.String(), activation) || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("invitation response disclosed a credential or was cacheable")
	}
	u, err := db.GetUserByEmail(recipient)
	if err != nil || u == nil || u.Role != "user" || u.IsVerified {
		t.Fatal("invitation did not create a pending regular user")
	}
	var stored string
	if err := db.Conn.QueryRow(`SELECT verification_token FROM users WHERE id=$1`, u.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == activation || len(stored) != 64 {
		t.Fatal("raw token was stored")
	}
	rr = do(t, h, "GET", "/api/admin/users?limit=1&page=1", session)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"has_more":true`) {
		t.Fatal("bounded account pagination failed")
	}
	for _, forbidden := range []string{u.PasswordHash, activation, "password_hash", "verification_token", "totp_secret"} {
		if strings.Contains(rr.Body.String(), forbidden) {
			t.Fatalf("account inventory disclosed %s", forbidden)
		}
	}
	const chosenPassword = "Recipient-Own-Password-9!"
	body, _ := json.Marshal(map[string]string{"token": activation, "password": chosenPassword})
	rr = doWithBody(t, h, "POST", "/api/auth/activate", "", strings.NewReader(string(body)))
	if rr.Code != 200 {
		t.Fatalf("activation: %d %s", rr.Code, rr.Body.String())
	}
	rr = doWithBody(t, h, "POST", "/api/auth/activate", "", strings.NewReader(string(body)))
	if rr.Code != 400 {
		t.Fatal("activation token replay succeeded")
	}
	u, err = db.GetUserByEmail(recipient)
	if err != nil || !u.IsVerified || u.Role != "user" || !auth.CheckPasswordHash(chosenPassword, u.PasswordHash) {
		t.Fatal("recipient password/verification/role mismatch")
	}
	newSession := loginTOTPToken(t, h, recipient, chosenPassword, "")
	if rr := do(t, h, "GET", "/api/admin/users", newSession); rr.Code != 403 {
		t.Fatal("invited user gained administrator access")
	}
}

func TestAdminInvitationReauthStrictInputAndDeliveryFailure(t *testing.T) {
	var tokens []string
	fail := true
	h, db := newAPI(t, func(cfg *Config) {
		cfg.SendInvitation = func(_, token string) error {
			tokens = append(tokens, token)
			if fail {
				return errors.New("simulated unavailable SMTP")
			}
			return nil
		}
	})
	const password = "Operator-Passphrase-9!"
	_, session := mkVerifiedUser(t, db, "owner@example.com", password, "admin")
	for _, body := range []string{
		`{"email":"new@example.com","password":"wrong"}`,
		`{"email":"new@example.com","password":"` + password + `","role":"admin"}`,
		`{"email":"new@example.com","password":"` + password + `"} {"role":"admin"}`,
	} {
		rr := doWithBody(t, h, "POST", "/api/admin/users", session, strings.NewReader(body))
		if rr.Code != 400 && rr.Code != 403 {
			t.Fatalf("unsafe body accepted: %d", rr.Code)
		}
	}
	if len(tokens) != 0 {
		t.Fatal("invalid requests sent invitations")
	}
	body := `{"email":"pending@example.com","password":"` + password + `"}`
	rr := doWithBody(t, h, "POST", "/api/admin/users", session, strings.NewReader(body))
	if rr.Code != 502 || !strings.Contains(rr.Body.String(), `"account_saved":true`) {
		t.Fatalf("mail failure hidden: %d %s", rr.Code, rr.Body.String())
	}
	u, _ := db.GetUserByEmail("pending@example.com")
	if u == nil || u.IsVerified {
		t.Fatal("pending account lost after mail failure")
	}
	rr = doWithBody(t, h, "POST", "/api/admin/users", session, strings.NewReader(body))
	if rr.Code != 409 || len(tokens) != 1 {
		t.Fatal("duplicate email overwrote an account or sent mail")
	}
	path := fmt.Sprintf("/api/admin/users/%d/invitation", u.ID)
	proof := `{"password":"` + password + `"}`
	rr = doWithBody(t, h, "POST", path, session, strings.NewReader(proof))
	if rr.Code != 429 || len(tokens) != 1 {
		t.Fatal("resend cooldown bypassed")
	}
	if _, err := db.Conn.Exec(`UPDATE users SET verification_expires_at=CURRENT_TIMESTAMP + INTERVAL '23 hours' WHERE id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	fail = false
	rr = doWithBody(t, h, "POST", path, session, strings.NewReader(proof))
	if rr.Code != 200 || len(tokens) != 2 || tokens[0] == tokens[1] {
		t.Fatalf("resend failed: %d", rr.Code)
	}
	if err := db.ActivateInvitedUser(t.Context(), tokens[0], "replay-hash"); err == nil {
		t.Fatal("resend left the old link valid")
	}
	if err := db.ActivateInvitedUser(t.Context(), tokens[1], "new-hash"); err != nil {
		t.Fatal(err)
	}
	rr = doWithBody(t, h, "POST", path, session, strings.NewReader(proof))
	if rr.Code != 409 || len(tokens) != 2 {
		t.Fatal("verified account received another activation link")
	}
}
