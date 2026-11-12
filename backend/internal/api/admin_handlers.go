package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"onion-spider/internal/auth"
	"onion-spider/internal/database"
)

func (d *deps) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset := ParsePagination(r)
	users, err := d.cfg.DB.ListAdminUsers(r.Context(), limit+1, offset)
	if err != nil {
		slog.ErrorContext(r.Context(), "admin_users_failed", "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Could not load accounts")
		return
	}
	hasMore := len(users) > limit
	if hasMore {
		users = users[:limit]
	}
	writeNoStoreJSON(w, http.StatusOK, map[string]any{"users": users, "has_more": hasMore})
}

func invitationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, database.ErrEmailInUse):
		WriteJSONError(w, http.StatusConflict, "This email already has an account. Pending accounts can be sent a new invitation.")
	case errors.Is(err, database.ErrCredentialsChanged):
		WriteJSONError(w, http.StatusConflict, "Your account permissions or credentials changed. Sign in again.")
	case errors.Is(err, database.ErrInvitationUnavailable):
		WriteJSONError(w, http.StatusConflict, "Only pending user accounts can receive an invitation.")
	case errors.Is(err, database.ErrInvitationCooldown):
		w.Header().Set("Retry-After", "300")
		WriteJSONError(w, http.StatusTooManyRequests, "Wait five minutes between invitations to the same account.")
	default:
		slog.ErrorContext(r.Context(), "admin_invitation_failed", "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Could not save the invitation")
	}
}

func (d *deps) deliverInvitation(w http.ResponseWriter, r *http.Request, id int, email, token string, created bool) {
	if err := d.cfg.SendInvitation(email, token); err != nil {
		slog.ErrorContext(r.Context(), "admin_invitation_delivery_failed", "uid", id, "err", err)
		d.logAuthEvent("admin_invitation_delivery_failed", email, ClientIP(r))
		writeNoStoreJSON(w, http.StatusBadGateway, map[string]any{
			"error":         "The pending account was saved, but email delivery could not be confirmed. If no message arrives, resend the invitation after five minutes.",
			"account_saved": true,
		})
		return
	}
	d.logAuthEvent("admin_invitation_sent", email, ClientIP(r))
	slog.InfoContext(r.Context(), "admin_invitation_sent", "actor", GetUserID(r), "uid", id)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeNoStoreJSON(w, status, map[string]any{"message": "Invitation sent. The recipient has 24 hours to choose a password.", "id": id})
}

func (d *deps) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeJSONBody(w, r, 2048, &req) {
		return
	}
	req.Email = database.NormalizeEmail(req.Email)
	if len(req.Email) > 254 || !EmailRegex.MatchString(req.Email) {
		WriteJSONError(w, http.StatusBadRequest, "Invalid email")
		return
	}
	actor := d.requireReauth(w, r, "admin_invite", req.Password, req.Code)
	if actor == nil {
		return
	}
	release, ok := d.acquirePasswordSlot(w)
	if !ok {
		return
	}
	hash, err := auth.HashPassword(auth.GenerateVerificationToken())
	release()
	if err != nil {
		WriteJSONError(w, http.StatusInternalServerError, "Could not prepare the account")
		return
	}
	token := auth.GenerateVerificationToken()
	id, err := d.cfg.DB.CreateInvitedUser(r.Context(), actor.ID, sessionVersion(r), d.cfg.RequireAdminMFA, req.Email, hash, token)
	if err != nil {
		invitationError(w, r, err)
		return
	}
	d.deliverInvitation(w, r, id, req.Email, token, true)
}

func (d *deps) handleAdminResendInvitation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		WriteJSONError(w, http.StatusBadRequest, "Invalid account")
		return
	}
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeJSONBody(w, r, 1024, &req) {
		return
	}
	actor := d.requireReauth(w, r, "admin_invite", req.Password, req.Code)
	if actor == nil {
		return
	}
	token := auth.GenerateVerificationToken()
	email, err := d.cfg.DB.RefreshInvitation(r.Context(), actor.ID, sessionVersion(r), d.cfg.RequireAdminMFA, id, token)
	if err != nil {
		invitationError(w, r, err)
		return
	}
	d.deliverInvitation(w, r, id, email, token, false)
}

func (d *deps) handleActivateAccount(w http.ResponseWriter, r *http.Request) {
	if !d.resetLim.Allow(r) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many attempts. Please try again later.")
		return
	}
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, 2048, &req) {
		return
	}
	if len(req.Token) != 64 || !TokenSafeRE.MatchString(req.Token) {
		WriteJSONError(w, http.StatusBadRequest, "Invalid activation link")
		return
	}
	if err := ValidatePassword(req.Password); err != nil {
		WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	release, ok := d.acquirePasswordSlot(w)
	if !ok {
		return
	}
	defer release()
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Password cannot be processed")
		return
	}
	if err := d.cfg.DB.ActivateInvitedUser(r.Context(), req.Token, hash); err != nil {
		if !errors.Is(err, database.ErrActivationInvalid) {
			slog.ErrorContext(r.Context(), "account_activation_failed", "err", err)
		}
		WriteJSONError(w, http.StatusBadRequest, "Activation link invalid, expired or already used. Ask the administrator for a new invitation.")
		return
	}
	slog.InfoContext(r.Context(), "account_activated")
	writeNoStoreJSON(w, http.StatusOK, map[string]string{"message": "Account activated. Sign in with your email and new password."})
}
