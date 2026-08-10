package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"onion-spider/internal/auth"
)

// SessionID returns the session handle carried by the request's token, or ""
// for tokens issued before sessions existed.
func SessionID(r *http.Request) string {
	claims, ok := r.Context().Value(userContextKey).(*auth.Claims)
	if !ok || claims == nil {
		return ""
	}
	return claims.SessionID
}

// handleSessions lists where the account is currently signed in.
func (d *deps) handleSessions(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	sessions, err := d.cfg.DB.ListSessions(uid, SessionID(r))
	if err != nil {
		slog.ErrorContext(r.Context(), "list_sessions_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	writeNoStoreJSON(w, http.StatusOK, sessions)
}

// handleRevokeSession ends one session by row ID. Ownership is enforced in the
// SQL, so a guessed ID belonging to another account simply matches nothing.
func (d *deps) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		WriteJSONError(w, http.StatusBadRequest, "Invalid session id")
		return
	}
	revoked, err := d.cfg.DB.RevokeSession(uid, id)
	if err != nil {
		slog.ErrorContext(r.Context(), "revoke_session_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if !revoked {
		// Same answer whether the row belongs to someone else or does not
		// exist: distinguishing them would confirm that a session ID is real.
		WriteJSONError(w, http.StatusNotFound, "Session not found")
		return
	}
	slog.InfoContext(r.Context(), "session_revoked", "uid", uid, "session", id)
	writeNoStoreJSON(w, http.StatusOK, map[string]string{"message": "Session signed out."})
}
