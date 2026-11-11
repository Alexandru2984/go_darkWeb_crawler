package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"onion-spider/internal/auth"
	"onion-spider/internal/database"
)

func passwordChange(t *testing.T, h http.Handler, token, current, replacement, code string) int {
	t.Helper()
	body, err := json.Marshal(map[string]string{"password": current, "new_password": replacement, "code": code})
	if err != nil {
		t.Fatal(err)
	}
	return doWithBody(t, h, http.MethodPost, "/api/auth/password", token, strings.NewReader(string(body))).Code
}

func TestPasswordChangeRequiresCurrentCredentialsAndRevokesEverySession(t *testing.T) {
	h, db := newAPI(t)
	const old = "Current-fixture-password-9!"
	const replacement = "Replacement-fixture-password-8!"
	uid, token := mkVerifiedUser(t, db, "password@example.com", old, "user")
	otherToken := loginTOTPToken(t, h, "password@example.com", old, "")
	for _, scenario := range []struct {
		name, token, current, replacement string
		want                              int
	}{
		{"no session", "", old, replacement, http.StatusUnauthorized},
		{"wrong password", token, "incorrect", replacement, http.StatusForbidden},
		{"weak password", token, old, "weak", http.StatusBadRequest},
		{"same password", token, old, old, http.StatusBadRequest},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := passwordChange(t, h, scenario.token, scenario.current, scenario.replacement, ""); got != scenario.want {
				t.Fatalf("status=%d want=%d", got, scenario.want)
			}
		})
	}
	if got := passwordChange(t, h, token, old, replacement, ""); got != http.StatusOK {
		t.Fatalf("change status=%d", got)
	}
	for _, prior := range []string{token, otherToken} {
		if rr := do(t, h, http.MethodGet, "/api/auth/me", prior); rr.Code != http.StatusUnauthorized {
			t.Fatal("pre-change session stayed authorized")
		}
	}
	user, err := db.GetUserByEmail("password@example.com")
	if err != nil || user == nil || user.ID != uid || !auth.CheckPasswordHash(replacement, user.PasswordHash) || auth.CheckPasswordHash(old, user.PasswordHash) {
		t.Fatal("stored password did not change correctly")
	}
	if rr := loginJSON(t, h, `{"email":"password@example.com","password":"`+old+`"}`); rr.Code != http.StatusUnauthorized {
		t.Fatal("old password still works")
	}
	_ = loginTOTPToken(t, h, "password@example.com", replacement, "")
}

func TestPasswordChangeRequiresMFAAndPreservesEnrolment(t *testing.T) {
	h, db := newAPI(t)
	const old = "Current-fixture-password-9!"
	const replacement = "Replacement-fixture-password-8!"
	uid, token := mkVerifiedUser(t, db, "password-mfa@example.com", old, "user")
	secret, recovery := enrolTOTP(t, h, token, old)
	token = loginTOTPToken(t, h, "password-mfa@example.com", old, nextTOTP(t, secret))
	for _, code := range []string{"", enrolmentTOTP(t, secret)} {
		if got := passwordChange(t, h, token, old, replacement, code); got != http.StatusForbidden {
			t.Fatalf("missing/replayed MFA code accepted: %d", got)
		}
	}
	if got := passwordChange(t, h, token, old, replacement, recovery[0]); got != http.StatusOK {
		t.Fatalf("change with recovery code status=%d", got)
	}
	state, err := db.GetTOTPState(uid)
	if err != nil || !state.Enabled || state.Secret != secret {
		t.Fatal("password change altered enrolled MFA")
	}
	if valid, err := db.ConsumeRecoveryCode(uid, database.RecoveryCodeHash(recovery[0])); err != nil || valid {
		t.Fatal("password-change recovery code was reusable")
	}
	_ = loginTOTPToken(t, h, "password-mfa@example.com", replacement, recovery[1])
}
