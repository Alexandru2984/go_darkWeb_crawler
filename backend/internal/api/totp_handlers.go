package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"onion-spider/internal/auth"
	"onion-spider/internal/database"
)

const totpIssuer = "Onion Spider"

// handleTOTPStatus reports whether the account has a second factor, so the UI
// can show enrolment or management without guessing.
func (d *deps) handleTOTPStatus(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	state, err := d.cfg.DB.GetTOTPState(uid)
	if err != nil {
		slog.ErrorContext(r.Context(), "totp_state_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	remaining := 0
	if state.Enabled {
		if remaining, err = d.cfg.DB.CountUnusedRecoveryCodes(uid); err != nil {
			slog.ErrorContext(r.Context(), "recovery_code_count_failed", "uid", uid, "err", err)
			WriteJSONError(w, http.StatusInternalServerError, "Internal error")
			return
		}
	}
	writeNoStoreJSON(w, http.StatusOK, map[string]any{
		"enabled":                  state.Enabled,
		"required":                 d.cfg.RequireAdminMFA && IsAdmin(r),
		"recovery_codes_remaining": remaining,
	})
}

// handleTOTPSetup stages a new secret and returns it once, with the otpauth URI
// an authenticator app scans. Nothing is enforced until handleTOTPConfirm
// proves the user can actually generate codes from it.
func (d *deps) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, 1024, &req) || !d.allowTOTPChange(w, r) {
		return
	}
	user, err := d.cfg.DB.GetUserByEmail(GetDBEmail(r))
	if err != nil || user == nil {
		slog.ErrorContext(r.Context(), "totp_setup_user_lookup_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		d.logAuthEvent("totp_setup_denied", user.Email, ClientIP(r))
		WriteJSONError(w, http.StatusForbidden, "Invalid credentials")
		return
	}
	secret := auth.GenerateTOTPSecret()
	if err := d.cfg.DB.StageTOTPSecret(uid, sessionVersion(r), secret); err != nil {
		if errors.Is(err, database.ErrTOTPStateChanged) {
			WriteJSONError(w, http.StatusConflict, "Two-factor settings or credentials changed. Sign in again and check your settings.")
		} else {
			slog.ErrorContext(r.Context(), "totp_setup_failed", "uid", uid, "err", err)
			WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		}
		return
	}
	slog.InfoContext(r.Context(), "totp_setup_started", "uid", uid)
	writeNoStoreJSON(w, http.StatusOK, map[string]string{
		"secret":           secret,
		"provisioning_uri": auth.TOTPProvisioningURI(secret, GetDBEmail(r), totpIssuer),
	})
}

// handleTOTPConfirm activates the second factor and hands back the recovery
// codes. This is the only time the plaintext codes exist outside the user's
// hands: only their digests are stored.
func (d *deps) handleTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	var req struct {
		Code string `json:"code"`
	}
	if !decodeJSONBody(w, r, 512, &req) || !d.allowTOTPChange(w, r) {
		return
	}

	state, err := d.cfg.DB.GetTOTPState(uid)
	if err != nil {
		slog.ErrorContext(r.Context(), "totp_state_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if state.Enabled {
		WriteJSONError(w, http.StatusConflict, "Two-factor authentication is already enabled")
		return
	}
	if state.Secret == "" {
		WriteJSONError(w, http.StatusBadRequest, "Start enrolment first")
		return
	}
	step, ok := auth.ValidateTOTP(state.Secret, req.Code, time.Now())
	if !ok {
		slog.InfoContext(r.Context(), "totp_confirm_failed", "uid", uid)
		WriteJSONError(w, http.StatusBadRequest, "That code is not valid. Check your device's clock and try the current code.")
		return
	}

	plain := make([]string, auth.RecoveryCodeCount)
	hashes := make([]string, auth.RecoveryCodeCount)
	for i := range plain {
		plain[i] = auth.GenerateRecoveryCode()
		hashes[i] = database.RecoveryCodeHash(plain[i])
	}
	if err := d.cfg.DB.ConfirmTOTP(uid, sessionVersion(r), state.Secret, int64(step), hashes); err != nil {
		if errors.Is(err, database.ErrTOTPStateChanged) {
			WriteJSONError(w, http.StatusConflict, "Two-factor settings or credentials changed. Sign in again and check your settings.")
		} else {
			slog.ErrorContext(r.Context(), "totp_confirm_store_failed", "uid", uid, "err", err)
			WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		}
		return
	}
	d.logAuthEvent("totp_enabled", GetDBEmail(r), ClientIP(r))
	slog.InfoContext(r.Context(), "totp_enabled", "uid", uid)
	clearSessionCookies(w, r)
	writeNoStoreJSON(w, http.StatusOK, map[string]any{
		"enabled":        true,
		"recovery_codes": plain,
		"message":        "Two-factor authentication is on. Save your recovery codes, then sign in again.",
	})
}

// handleTOTPDisable turns the second factor off. It demands the password and a
// current code: an attacker holding a live session should not be able to strip
// the very control that is meant to survive a stolen session.
func (d *deps) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeJSONBody(w, r, 1024, &req) || !d.allowTOTPChange(w, r) {
		return
	}

	user, err := d.cfg.DB.GetUserByEmail(GetDBEmail(r))
	if err != nil || user == nil {
		slog.ErrorContext(r.Context(), "totp_disable_user_lookup_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		d.logAuthEvent("totp_disable_fail", user.Email, ClientIP(r))
		WriteJSONError(w, http.StatusForbidden, "Invalid credentials")
		return
	}
	state, err := d.cfg.DB.GetTOTPState(uid)
	if err != nil {
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if !state.Enabled {
		WriteJSONError(w, http.StatusBadRequest, "Two-factor authentication is not enabled")
		return
	}
	if !d.verifySecondFactor(uid, state, req.Code) {
		d.logAuthEvent("totp_disable_fail", user.Email, ClientIP(r))
		WriteJSONError(w, http.StatusForbidden, "Invalid credentials")
		return
	}
	if err := d.cfg.DB.DisableTOTP(uid, sessionVersion(r)); err != nil {
		if errors.Is(err, database.ErrTOTPStateChanged) {
			WriteJSONError(w, http.StatusConflict, "Two-factor settings or credentials changed. Sign in again and check your settings.")
		} else {
			slog.ErrorContext(r.Context(), "totp_disable_failed", "uid", uid, "err", err)
			WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		}
		return
	}
	d.logAuthEvent("totp_disabled", user.Email, ClientIP(r))
	slog.InfoContext(r.Context(), "totp_disabled", "uid", uid)
	clearSessionCookies(w, r)
	writeNoStoreJSON(w, http.StatusOK, map[string]string{"message": "Two-factor authentication is off. Sign in again."})
}

func (d *deps) allowTOTPChange(w http.ResponseWriter, r *http.Request) bool {
	if d.sensitiveLim.Allow(RequestKey(r)) {
		return true
	}
	WriteJSONError(w, http.StatusTooManyRequests, "Too many attempts. Please try again later.")
	return false
}

// verifySecondFactor accepts either a current TOTP code or an unused recovery
// code, consuming whichever matched so it cannot be presented twice.
func (d *deps) verifySecondFactor(userID int, state database.TOTPState, code string) bool {
	if code == "" {
		return false
	}
	if step, ok := auth.ValidateTOTP(state.Secret, code, time.Now()); ok {
		if err := d.cfg.DB.ConsumeTOTPStep(userID, state.Secret, int64(step)); err != nil {
			// A replay is a failed authentication, not a server error.
			if !errors.Is(err, database.ErrTOTPReplay) {
				slog.Error("totp_consume_failed", "uid", userID, "err", err)
			}
			return false
		}
		return true
	}
	used, err := d.cfg.DB.ConsumeRecoveryCode(userID, database.RecoveryCodeHash(auth.NormalizeRecoveryCode(code)))
	if err != nil {
		slog.Error("recovery_code_consume_failed", "uid", userID, "err", err)
		return false
	}
	return used
}
