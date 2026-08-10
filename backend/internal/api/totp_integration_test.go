package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"onion-spider/internal/auth"
	"onion-spider/internal/database"
)

// enrolTOTP walks an account through setup and confirmation, returning the
// secret and the recovery codes handed out once at the end.
func enrolTOTP(t *testing.T, h http.Handler, token string) (secret string, recovery []string) {
	t.Helper()

	rr := doWithBody(t, h, http.MethodPost, "/api/auth/totp/setup", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("totp setup: got %d, body=%s", rr.Code, rr.Body.String())
	}
	var setup struct {
		Secret          string `json:"secret"`
		ProvisioningURI string `json:"provisioning_uri"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &setup); err != nil {
		t.Fatalf("decode setup: %v", err)
	}
	if setup.Secret == "" || !strings.HasPrefix(setup.ProvisioningURI, "otpauth://totp/") {
		t.Fatalf("setup returned an unusable enrolment: %+v", setup)
	}

	code := enrolmentTOTP(t, setup.Secret)
	rr = doWithBody(t, h, http.MethodPost, "/api/auth/totp/confirm", token,
		strings.NewReader(`{"code":"`+code+`"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("totp confirm: got %d, body=%s", rr.Code, rr.Body.String())
	}
	var confirmed struct {
		Enabled       bool     `json:"enabled"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &confirmed); err != nil {
		t.Fatalf("decode confirm: %v", err)
	}
	if !confirmed.Enabled || len(confirmed.RecoveryCodes) != auth.RecoveryCodeCount {
		t.Fatalf("confirm returned %+v, want enabled with %d codes", confirmed, auth.RecoveryCodeCount)
	}
	return setup.Secret, confirmed.RecoveryCodes
}

// totpCodeAt returns the code an authenticator shows `offset` from now.
func totpCodeAt(t *testing.T, secret string, offset time.Duration) string {
	t.Helper()
	code, err := auth.GenerateTOTPCode(secret, time.Now().Add(offset))
	if err != nil {
		t.Fatalf("GenerateTOTPCode: %v", err)
	}
	return code
}

// enrolmentTOTP is the code a user reads off their app while enrolling: the
// current one. Confirmation consumes that step.
func enrolmentTOTP(t *testing.T, secret string) string {
	t.Helper()
	return totpCodeAt(t, secret, 0)
}

// nextTOTP is a code from the following period. Tests need it because every
// accepted code moves the replay watermark forward, so the step just spent
// during enrolment cannot be reused. One period ahead is still inside the
// accepted drift window, which is why a real user logging in seconds after
// enrolling is not locked out either.
func nextTOTP(t *testing.T, secret string) string {
	t.Helper()
	return totpCodeAt(t, secret, auth.TOTPPeriod)
}

// loginJSON posts a login body and returns the recorder.
func loginJSON(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// mkVerifiedUser creates a verified account with a known password.
func mkVerifiedUser(t *testing.T, db *database.DB, email, password, role string) (int, string) {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := db.CreateUser(email, hash, role, "verification-token-"+email); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := db.Conn.Exec(`UPDATE users SET is_verified=TRUE WHERE email=$1`, email); err != nil {
		t.Fatalf("verify fixture: %v", err)
	}
	u, err := db.GetUserByEmail(email)
	if err != nil || u == nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	// Mint a token backed by a real session row. A handle that resolves to
	// nothing is rejected on every authenticated request, so this fixture has
	// to go through the same path a login does.
	sid := auth.NewSessionID()
	if _, err := db.CreateSession(u.ID, sid, "Test client", time.Now().Add(auth.TokenTTL)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	tok, err := auth.GenerateToken(u.ID, u.TokenVersion, sid)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	return u.ID, tok
}

func TestTOTPLoginRequiresTheSecondFactor(t *testing.T) {
	h, db := newAPI(t)
	const (
		emailAddress = "mfa@example.com"
		password     = "Correct-Horse-9!"
	)
	_, token := mkVerifiedUser(t, db, emailAddress, password, "user")
	secret, _ := enrolTOTP(t, h, token)

	// Correct password alone is no longer enough, and the response says so
	// without spending a login-failure budget.
	rr := loginJSON(t, h, `{"email":"`+emailAddress+`","password":"`+password+`"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("password-only login: got %d, want 401", rr.Code)
	}
	var challenge map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	if challenge["totp_required"] != true {
		t.Fatalf("response does not signal that a code is needed: %v", challenge)
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Fatal("a session cookie was issued before the second factor was proved")
	}

	// A wrong code is refused.
	if rr := loginJSON(t, h, `{"email":"`+emailAddress+`","password":"`+password+`","code":"000000"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code: got %d, want 401", rr.Code)
	}

	// The real code completes the login.
	code := nextTOTP(t, secret)
	rr = loginJSON(t, h, `{"email":"`+emailAddress+`","password":"`+password+`","code":"`+code+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("login with code: got %d, body=%s", rr.Code, rr.Body.String())
	}
	if len(rr.Result().Cookies()) != 2 {
		t.Fatalf("expected session and CSRF cookies, got %d", len(rr.Result().Cookies()))
	}
}

func TestTOTPCodeCannotBeReplayed(t *testing.T) {
	// A code stays cryptographically valid for its whole period plus drift.
	// Without a consumed-step record, anyone who observed one could reuse it.
	h, db := newAPI(t)
	const (
		emailAddress = "replay@example.com"
		password     = "Correct-Horse-9!"
	)
	_, token := mkVerifiedUser(t, db, emailAddress, password, "user")
	secret, _ := enrolTOTP(t, h, token)

	code := nextTOTP(t, secret)
	body := `{"email":"` + emailAddress + `","password":"` + password + `","code":"` + code + `"}`
	if rr := loginJSON(t, h, body); rr.Code != http.StatusOK {
		t.Fatalf("first use of the code: got %d, body=%s", rr.Code, rr.Body.String())
	}
	if rr := loginJSON(t, h, body); rr.Code != http.StatusUnauthorized {
		t.Fatalf("replayed code was accepted: got %d", rr.Code)
	}
}

func TestRecoveryCodeWorksOnceAndOnlyForItsOwner(t *testing.T) {
	h, db := newAPI(t)
	const (
		ownerEmail = "recovery@example.com"
		otherEmail = "bystander@example.com"
		password   = "Correct-Horse-9!"
	)
	_, ownerToken := mkVerifiedUser(t, db, ownerEmail, password, "user")
	_, otherToken := mkVerifiedUser(t, db, otherEmail, password, "user")
	_, recovery := enrolTOTP(t, h, ownerToken)
	enrolTOTP(t, h, otherToken)

	// Another account cannot spend this account's recovery code.
	stolen := `{"email":"` + otherEmail + `","password":"` + password + `","code":"` + recovery[0] + `"}`
	if rr := loginJSON(t, h, stolen); rr.Code != http.StatusUnauthorized {
		t.Fatalf("a recovery code authenticated a different account: got %d", rr.Code)
	}

	body := `{"email":"` + ownerEmail + `","password":"` + password + `","code":"` + recovery[0] + `"}`
	if rr := loginJSON(t, h, body); rr.Code != http.StatusOK {
		t.Fatalf("recovery code login: got %d, body=%s", rr.Code, rr.Body.String())
	}
	if rr := loginJSON(t, h, body); rr.Code != http.StatusUnauthorized {
		t.Fatalf("recovery code was reusable: got %d", rr.Code)
	}
	// A different, unused code still works.
	second := `{"email":"` + ownerEmail + `","password":"` + password + `","code":"` + recovery[1] + `"}`
	if rr := loginJSON(t, h, second); rr.Code != http.StatusOK {
		t.Fatalf("second recovery code: got %d", rr.Code)
	}
}

func TestAdminEndpointsRequireEnrolledSecondFactor(t *testing.T) {
	h, db := newAPIWithConfig(t, func(c *Config) { c.RequireAdminMFA = true })
	const (
		adminEmail = "admin-mfa@example.com"
		password   = "Correct-Horse-9!"
	)
	_, adminToken := mkVerifiedUser(t, db, adminEmail, password, "admin")

	// Administrative action is refused while the second factor is missing.
	rr := do(t, h, http.MethodGet, "/api/blacklist", adminToken)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("admin without MFA: got %d, want 403", rr.Code)
	}

	// Enrolment itself must stay reachable, or the requirement is a lockout.
	enrolTOTP(t, h, adminToken)

	if rr := do(t, h, http.MethodGet, "/api/blacklist", adminToken); rr.Code != http.StatusOK {
		t.Fatalf("admin after enrolling: got %d, body=%s", rr.Code, rr.Body.String())
	}
	// Ordinary authenticated endpoints were never gated.
	if rr := do(t, h, http.MethodGet, "/api/nodes", adminToken); rr.Code != http.StatusOK {
		t.Fatalf("non-admin endpoint was gated: got %d", rr.Code)
	}
	_ = db
}

func TestTOTPCannotBeDisabledWithoutPasswordAndCode(t *testing.T) {
	// The point of a second factor is to survive a stolen session, so a live
	// session alone must not be able to remove it.
	h, db := newAPI(t)
	const (
		emailAddress = "disable@example.com"
		password     = "Correct-Horse-9!"
	)
	_, token := mkVerifiedUser(t, db, emailAddress, password, "user")
	secret, _ := enrolTOTP(t, h, token)

	rr := doWithBody(t, h, http.MethodPost, "/api/auth/totp/disable", token,
		strings.NewReader(`{"password":"wrong-password","code":"000000"}`))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("disable with wrong password: got %d, want 401", rr.Code)
	}
	rr = doWithBody(t, h, http.MethodPost, "/api/auth/totp/disable", token,
		strings.NewReader(`{"password":"`+password+`","code":"000000"}`))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("disable with wrong code: got %d, want 401", rr.Code)
	}

	rr = doWithBody(t, h, http.MethodPost, "/api/auth/totp/disable", token,
		strings.NewReader(`{"password":"`+password+`","code":"`+nextTOTP(t, secret)+`"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("disable with both factors: got %d, body=%s", rr.Code, rr.Body.String())
	}
	// With the factor gone, the password alone logs in again.
	if rr := loginJSON(t, h, `{"email":"`+emailAddress+`","password":"`+password+`"}`); rr.Code != http.StatusOK {
		t.Fatalf("login after disabling: got %d", rr.Code)
	}
}

func TestEnrolmentCannotBeHijackedWhileActive(t *testing.T) {
	// Re-running setup on an account that already has a second factor would let
	// a stolen session swap in an authenticator it controls.
	h, db := newAPI(t)
	_, token := mkVerifiedUser(t, db, "hijack@example.com", "Correct-Horse-9!", "user")
	enrolTOTP(t, h, token)

	if rr := doWithBody(t, h, http.MethodPost, "/api/auth/totp/setup", token, nil); rr.Code != http.StatusConflict {
		t.Fatalf("re-enrolment while active: got %d, want 409", rr.Code)
	}
}

func TestRevokingOneSessionLeavesTheOthersAlone(t *testing.T) {
	// The whole point of the sessions table: before it, the only lever was
	// token_version, which signs every device out at once.
	h, db := newAPI(t)
	const (
		emailAddress = "sessions@example.com"
		password     = "Correct-Horse-9!"
	)
	uid, phone := mkVerifiedUser(t, db, emailAddress, password, "user")

	sid := auth.NewSessionID()
	if _, err := db.CreateSession(uid, sid, "Firefox on Linux", time.Now().Add(auth.TokenTTL)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	laptop, err := auth.GenerateToken(uid, 0, sid)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// Both devices work, and each sees both sessions listed.
	for name, tok := range map[string]string{"phone": phone, "laptop": laptop} {
		if rr := do(t, h, http.MethodGet, "/api/nodes", tok); rr.Code != http.StatusOK {
			t.Fatalf("%s before revocation: got %d", name, rr.Code)
		}
	}
	rr := do(t, h, http.MethodGet, "/api/auth/sessions", laptop)
	if rr.Code != http.StatusOK {
		t.Fatalf("list sessions: got %d, body=%s", rr.Code, rr.Body.String())
	}
	var sessions []struct {
		ID          int    `json:"id"`
		DeviceLabel string `json:"device_label"`
		Current     bool   `json:"current"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %+v", len(sessions), sessions)
	}

	// Exactly one row is flagged as the caller's own.
	var target int
	current := 0
	for _, s := range sessions {
		if s.Current {
			current++
		} else {
			target = s.ID
		}
	}
	if current != 1 {
		t.Fatalf("expected exactly one session marked current, got %d", current)
	}

	// Revoke the other device from this one.
	if rr := do(t, h, http.MethodDelete, "/api/auth/sessions/"+strconv.Itoa(target), laptop); rr.Code != http.StatusOK {
		t.Fatalf("revoke: got %d, body=%s", rr.Code, rr.Body.String())
	}
	if rr := do(t, h, http.MethodGet, "/api/nodes", phone); rr.Code != http.StatusUnauthorized {
		t.Fatalf("revoked device still authenticated: got %d", rr.Code)
	}
	if rr := do(t, h, http.MethodGet, "/api/nodes", laptop); rr.Code != http.StatusOK {
		t.Fatalf("revoking one device signed out the other: got %d", rr.Code)
	}
}

func TestSessionsCannotBeRevokedAcrossAccounts(t *testing.T) {
	h, db := newAPI(t)
	const password = "Correct-Horse-9!"
	victimID, victimToken := mkVerifiedUser(t, db, "victim@example.com", password, "user")
	_, attackerToken := mkVerifiedUser(t, db, "attacker@example.com", password, "user")

	sessions, err := db.ListSessions(victimID, "")
	if err != nil || len(sessions) == 0 {
		t.Fatalf("victim sessions: %v (%d)", err, len(sessions))
	}
	victimSession := sessions[0].ID

	rr := do(t, h, http.MethodDelete, "/api/auth/sessions/"+strconv.Itoa(victimSession), attackerToken)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-account revoke: got %d, want 404", rr.Code)
	}
	if rr := do(t, h, http.MethodGet, "/api/nodes", victimToken); rr.Code != http.StatusOK {
		t.Fatalf("victim was signed out by another account: got %d", rr.Code)
	}
}

func TestLogoutRevokesTheSessionNotJustTheCookie(t *testing.T) {
	// Clearing a cookie only stops one browser sending the token. A copy of
	// that token must stop working too.
	h, db := newAPI(t)
	_, token := mkVerifiedUser(t, db, "logout@example.com", "Correct-Horse-9!", "user")

	if rr := do(t, h, http.MethodPost, "/api/auth/logout", token); rr.Code != http.StatusOK {
		t.Fatalf("logout: got %d", rr.Code)
	}
	if rr := do(t, h, http.MethodGet, "/api/nodes", token); rr.Code != http.StatusUnauthorized {
		t.Fatalf("token still worked after logout: got %d", rr.Code)
	}
}

func TestLegacyTokensWithoutASessionStillWork(t *testing.T) {
	// Tokens minted before this feature carry no session handle. Rejecting them
	// would sign every user out on deploy to enforce something none of their
	// tokens could have carried.
	h, db := newAPI(t)
	uid, _ := mkVerifiedUser(t, db, "legacy-session@example.com", "Correct-Horse-9!", "user")
	legacy, err := auth.GenerateToken(uid, 0, "")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if rr := do(t, h, http.MethodGet, "/api/nodes", legacy); rr.Code != http.StatusOK {
		t.Fatalf("legacy token rejected: got %d, body=%s", rr.Code, rr.Body.String())
	}
}
