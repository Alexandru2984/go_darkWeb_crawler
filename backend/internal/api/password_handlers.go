package api

import (
	"errors"
	"log/slog"
	"net/http"

	"onion-spider/internal/auth"
	"onion-spider/internal/database"
)

func (d *deps) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password    string `json:"password"`
		NewPassword string `json:"new_password"`
		Code        string `json:"code"`
	}
	if !decodeJSONBody(w, r, 4096, &req) || !d.allowTOTPChange(w, r) {
		return
	}
	if err := ValidatePassword(req.NewPassword); err != nil {
		WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.NewPassword == req.Password {
		WriteJSONError(w, http.StatusBadRequest, "Choose a password different from your current password")
		return
	}
	release, ok := d.acquirePasswordSlot(w)
	if !ok {
		return
	}
	defer release()

	user, err := d.reauthenticate(r, req.Password, req.Code)
	if err != nil {
		if errors.Is(err, errReauthFailed) {
			d.logAuthEvent("password_change_denied", GetDBEmail(r), ClientIP(r))
			WriteJSONError(w, http.StatusForbidden, "Invalid credentials")
		} else {
			slog.ErrorContext(r.Context(), "password_change_reauth_failed", "uid", GetUserID(r), "err", err)
			WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		}
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		WriteJSONError(w, http.StatusInternalServerError, "Password cannot be processed")
		return
	}
	if err := d.cfg.DB.ChangePassword(user.ID, sessionVersion(r), user.PasswordHash, hash); err != nil {
		if errors.Is(err, database.ErrCredentialsChanged) {
			WriteJSONError(w, http.StatusConflict, "Account credentials changed. Sign in again before retrying.")
		} else {
			slog.ErrorContext(r.Context(), "password_change_failed", "uid", user.ID, "err", err)
			WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		}
		return
	}
	d.logAuthEvent("password_changed", user.Email, ClientIP(r))
	slog.InfoContext(r.Context(), "password_changed", "uid", user.ID)
	clearSessionCookies(w, r)
	writeNoStoreJSON(w, http.StatusOK, map[string]string{
		"message": "Password updated. Every device was signed out. Sign in with your new password.",
	})
}
