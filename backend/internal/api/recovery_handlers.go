package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"onion-spider/internal/auth"
	"onion-spider/internal/database"
)

// Recovery-code replacement requires the actual authenticator. An existing
// recovery code cannot mint a replacement set; lost authenticators use the
// separate password + recovery-code disable/re-enrol flow.
func (d *deps) handleRegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeJSONBody(w, r, 2048, &req) || !d.allowTOTPChange(w, r) {
		return
	}
	release, ok := d.acquirePasswordSlot(w)
	if !ok {
		return
	}
	defer release()
	uid := GetUserID(r)
	user, err := d.cfg.DB.GetUserByEmail(GetDBEmail(r))
	if err != nil || user == nil || user.ID != uid {
		slog.ErrorContext(r.Context(), "recovery_regeneration_lookup_failed", "uid", uid)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		d.logAuthEvent("recovery_regeneration_denied", user.Email, ClientIP(r))
		WriteJSONError(w, http.StatusForbidden, "Invalid credentials")
		return
	}
	state, err := d.cfg.DB.GetTOTPState(uid)
	if err != nil {
		slog.ErrorContext(r.Context(), "recovery_regeneration_state_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if !state.Enabled {
		WriteJSONError(w, http.StatusBadRequest, "Enable two-factor authentication first")
		return
	}
	step, valid := auth.ValidateTOTP(state.Secret, req.Code, time.Now())
	if !valid {
		d.logAuthEvent("recovery_regeneration_denied", user.Email, ClientIP(r))
		WriteJSONError(w, http.StatusForbidden, "Invalid credentials")
		return
	}
	plain := make([]string, auth.RecoveryCodeCount)
	hashes := make([]string, auth.RecoveryCodeCount)
	for i := range plain {
		plain[i] = auth.GenerateRecoveryCode()
		hashes[i] = database.RecoveryCodeHash(plain[i])
	}
	if err := d.cfg.DB.RegenerateRecoveryCodes(uid, sessionVersion(r), state.Secret, int64(step), hashes); err != nil {
		if errors.Is(err, database.ErrTOTPReplay) || errors.Is(err, database.ErrTOTPStateChanged) {
			WriteJSONError(w, http.StatusConflict, "Credentials changed or that code was already used. Sign in again and use a fresh authenticator code.")
		} else {
			slog.ErrorContext(r.Context(), "recovery_regeneration_failed", "uid", uid, "err", err)
			WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		}
		return
	}
	d.logAuthEvent("recovery_codes_replaced", user.Email, ClientIP(r))
	slog.InfoContext(r.Context(), "recovery_codes_replaced", "uid", uid)
	clearSessionCookies(w, r)
	writeNoStoreJSON(w, http.StatusOK, map[string]any{
		"recovery_codes": plain,
		"message":        "Recovery codes replaced. Old codes no longer work. Save the new codes before signing in again.",
	})
}
