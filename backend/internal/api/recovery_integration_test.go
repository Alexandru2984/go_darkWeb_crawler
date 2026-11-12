package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"onion-spider/internal/auth"
	"onion-spider/internal/database"
)

func replaceRecoveryRequest(t *testing.T, handler http.Handler, token, password, code string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"password": password, "code": code})
	if err != nil {
		t.Fatal(err)
	}
	return doWithBody(t, handler, http.MethodPost, "/api/auth/totp/recovery", token, strings.NewReader(string(raw)))
}

func TestRecoveryReplacementRequiresAuthenticatorAndRevokesOldCodesAndSessions(t *testing.T) {
	h, db := newAPI(t)
	const password = "Recovery-fixture-password-9!"
	uid, token := mkVerifiedUser(t, db, "replace@example.com", password, "user")
	secret, previous := enrolTOTP(t, h, token, password)
	token = loginTOTPToken(t, h, "replace@example.com", password, previous[0])
	before, err := db.GetTOTPState(uid)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := auth.GenerateTOTPCode(secret, time.Unix(before.LastStep*int64(auth.TOTPPeriod/time.Second), 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name, token, password, code string
		status                      int
	}{
		{"no session", "", password, nextTOTP(t, secret), http.StatusUnauthorized},
		{"wrong password", token, "wrong", nextTOTP(t, secret), http.StatusForbidden},
		{"missing code", token, password, "", http.StatusForbidden},
		{"recovery code cannot mint replacements", token, password, previous[1], http.StatusForbidden},
		{"replayed authenticator code", token, password, replayed, http.StatusConflict},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if rr := replaceRecoveryRequest(t, h, scenario.token, scenario.password, scenario.code); rr.Code != scenario.status {
				t.Fatalf("status=%d expected=%d", rr.Code, scenario.status)
			}
		})
	}
	remaining, err := db.CountUnusedRecoveryCodes(uid)
	if err != nil || remaining != auth.RecoveryCodeCount-1 {
		t.Fatal("rejected replacement consumed or replaced recovery credentials")
	}
	rr := replaceRecoveryRequest(t, h, token, password, nextTOTP(t, secret))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("replacement status=%d", rr.Code)
	}
	var result struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil || len(result.RecoveryCodes) != auth.RecoveryCodeCount {
		t.Fatal("replacement did not return a complete one-time code set")
	}
	newSet := map[string]bool{}
	for _, code := range result.RecoveryCodes {
		if len(code) != 31 || newSet[code] {
			t.Fatal("replacement codes are too short or repeated")
		}
		newSet[code] = true
	}
	if response := do(t, h, http.MethodGet, "/api/auth/me", token); response.Code != http.StatusUnauthorized {
		t.Fatal("pre-replacement session remained authorized")
	}
	for _, old := range previous {
		valid, err := db.ConsumeRecoveryCode(uid, database.RecoveryCodeHash(old))
		if err != nil || valid {
			t.Fatal("old recovery credential remained valid")
		}
	}
	state, err := db.GetTOTPState(uid)
	if err != nil || !state.Enabled || !state.Confirmed || state.Secret != secret || state.LastStep <= before.LastStep {
		t.Fatal("replacement changed the authenticator or failed to consume its code")
	}
	var plaintextStored int
	if err := db.Conn.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE user_id=$1 AND length(code_hash)<>64`, uid).Scan(&plaintextStored); err != nil || plaintextStored != 0 {
		t.Fatal("recovery material was not stored as digests")
	}
	_ = loginTOTPToken(t, h, "replace@example.com", password, result.RecoveryCodes[0])
}

func TestRecoveryReplacementRequiresEnrolledMFA(t *testing.T) {
	h, db := newAPI(t)
	const password = "Recovery-fixture-password-9!"
	_, token := mkVerifiedUser(t, db, "no-mfa@example.com", password, "user")
	if rr := replaceRecoveryRequest(t, h, token, password, "123456"); rr.Code != http.StatusBadRequest {
		t.Fatalf("replacement without MFA status=%d", rr.Code)
	}
}

func TestLegacyPrintedRecoveryCodesRemainUsableUntilReplacement(t *testing.T) {
	h, db := newAPI(t)
	const password = "Recovery-fixture-password-9!"
	uid, token := mkVerifiedUser(t, db, "legacy-recovery@example.com", password, "user")
	_, _ = enrolTOTP(t, h, token, password)
	const legacy = "abcde-fghjk"
	if _, err := db.Conn.Exec(`DELETE FROM recovery_codes WHERE user_id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn.Exec(`INSERT INTO recovery_codes (user_id,code_hash) VALUES ($1,$2)`, uid, database.RecoveryCodeHash(legacy)); err != nil {
		t.Fatal(err)
	}
	_ = loginTOTPToken(t, h, "legacy-recovery@example.com", password, " ABCDE-FGHJK ")
	response := loginJSON(t, h, `{"email":"legacy-recovery@example.com","password":"`+password+`","code":"`+legacy+`"}`)
	if response.Code != http.StatusUnauthorized {
		t.Fatal("legacy recovery code was reusable")
	}
}
