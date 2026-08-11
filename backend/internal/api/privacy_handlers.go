package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"onion-spider/internal/auth"
	"onion-spider/internal/database"
	"onion-spider/internal/email"
)

// errReauthFailed means the caller did not prove they hold the account's
// credentials. It is deliberately indistinguishable between a wrong password
// and a wrong second factor.
var errReauthFailed = errors.New("re-authentication failed")

// reauthenticate re-proves the account's credentials for an action a live
// session alone should not be enough to perform.
//
// Everything in this file destroys data that cannot be recovered. A session
// cookie is a bearer credential: whoever holds it acts as the user for as long
// as it lives, which is exactly the situation where an attacker would reach for
// "delete everything" — either to cover their tracks or as the payload itself.
// Demanding the password, and the second factor when one is enrolled, means a
// stolen session cannot do it, and that is the whole point of the grace period
// that follows.
func (d *deps) reauthenticate(r *http.Request, password, code string) (*database.User, error) {
	uid := GetUserID(r)
	user, err := d.cfg.DB.GetUserByEmail(GetDBEmail(r))
	if err != nil {
		return nil, err
	}
	if user == nil || user.ID != uid {
		return nil, errReauthFailed
	}
	if !auth.CheckPasswordHash(password, user.PasswordHash) {
		return nil, errReauthFailed
	}
	state, err := d.cfg.DB.GetTOTPState(uid)
	if err != nil {
		return nil, err
	}
	if state.Enabled && !d.verifySecondFactor(uid, state, code) {
		return nil, errReauthFailed
	}
	return user, nil
}

// requireReauth wraps reauthenticate with the shared rate limit, audit line and
// error responses. Returns nil when the caller should stop.
//
// The limiter matters here beyond the usual load argument: these endpoints take
// a password and say whether it was right, which makes them an online guessing
// oracle for anyone who already holds a session. The unauthenticated login path
// has an account lockout for that; this is its equivalent.
func (d *deps) requireReauth(w http.ResponseWriter, r *http.Request, action, password, code string) *database.User {
	if !d.sensitiveLim.Allow(RequestKey(r)) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many attempts. Please try again later.")
		return nil
	}
	user, err := d.reauthenticate(r, password, code)
	if err != nil {
		if errors.Is(err, errReauthFailed) {
			d.logAuthEvent(action+"_denied", GetDBEmail(r), ClientIP(r))
			slog.InfoContext(r.Context(), "reauth_denied", "uid", GetUserID(r), "action", action)
			WriteJSONError(w, http.StatusUnauthorized, "Invalid credentials")
			return nil
		}
		slog.ErrorContext(r.Context(), "reauth_failed", "uid", GetUserID(r), "action", action, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return nil
	}
	return user
}

// handlePrivacySettings reports the account's data policy.
func (d *deps) handlePrivacySettings(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	settings, err := d.cfg.DB.GetPrivacySettings(uid)
	if err != nil {
		slog.ErrorContext(r.Context(), "privacy_settings_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	writeNoStoreJSON(w, http.StatusOK, map[string]any{
		"settings":           settings,
		"max_retention_days": database.MaxRetentionDays,
		"deletion_grace":     int(d.cfg.DeletionGrace / (24 * time.Hour)),
	})
}

// handlePrivacySettingsUpdate writes the retention window and the metadata-only
// switch.
func (d *deps) handlePrivacySettingsUpdate(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	var req struct {
		RetentionDays int  `json:"retention_days"`
		StoreContent  bool `json:"store_content"`
	}
	if !decodeJSONBody(w, r, 512, &req) {
		return
	}
	if err := d.cfg.DB.SetPrivacySettings(uid, req.RetentionDays, req.StoreContent); err != nil {
		if errors.Is(err, database.ErrRetentionOutOfRange) {
			WriteJSONError(w, http.StatusBadRequest, "Retention must be between 0 and 3650 days (0 keeps records indefinitely)")
			return
		}
		slog.ErrorContext(r.Context(), "privacy_settings_update_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	slog.InfoContext(r.Context(), "privacy_settings_updated", "uid", uid,
		"retention_days", req.RetentionDays, "store_content", req.StoreContent)
	settings, err := d.cfg.DB.GetPrivacySettings(uid)
	if err != nil {
		slog.ErrorContext(r.Context(), "privacy_settings_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	writeNoStoreJSON(w, http.StatusOK, map[string]any{
		"settings": settings,
		"message":  "Data settings saved.",
	})
}

// privacyExportVersion identifies the document shape, so a consumer reading an
// export produced by an older deployment can tell what to expect.
const privacyExportVersion = 1

// handlePrivacyExport streams everything this service holds about the account.
//
// The small sections are gathered before a single byte is written. Once the
// response body has started there is no way to change the status code, so a
// failure mid-stream can only be logged and leaves the caller holding a
// truncated document that still claims to be a 200. Doing the fallible lookups
// up front means the ordinary failures return an honest error, and only the two
// unbounded sections — which have to stream — carry that risk.
func (d *deps) handlePrivacyExport(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	release, ok := d.acquireExportSlot(w, uid)
	if !ok {
		return
	}
	defer release()

	profile, err := d.cfg.DB.GetAccountProfile(uid)
	if err != nil {
		slog.ErrorContext(r.Context(), "export_profile_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	settings, err := d.cfg.DB.GetPrivacySettings(uid)
	if err != nil {
		slog.ErrorContext(r.Context(), "export_settings_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	sessions, err := d.cfg.DB.ListSessions(uid, SessionID(r))
	if err != nil {
		slog.ErrorContext(r.Context(), "export_sessions_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	history, err := d.cfg.DB.ListAuthEvents(auditEmailReference(profile.Email), 1000)
	if err != nil {
		slog.ErrorContext(r.Context(), "export_history_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	recoveryRemaining, err := d.cfg.DB.CountUnusedRecoveryCodes(uid)
	if err != nil {
		slog.ErrorContext(r.Context(), "export_recovery_count_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}

	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", "attachment; filename=onion_spider_personal_data.json")
	slog.InfoContext(r.Context(), "privacy_export", "uid", uid)

	enc := json.NewEncoder(w)
	writeSection := func(name string, v any) error {
		if _, err := w.Write([]byte(`"` + name + `":`)); err != nil {
			return err
		}
		if err := enc.Encode(v); err != nil {
			return err
		}
		_, err := w.Write([]byte(","))
		return err
	}

	w.Write([]byte("{"))
	writeSection("format_version", privacyExportVersion)
	writeSection("generated_at", time.Now().UTC().Format(time.RFC3339))
	writeSection("account", profile)
	writeSection("privacy_settings", settings)
	writeSection("sessions", sessions)
	writeSection("recovery_codes_unused", recoveryRemaining)
	writeSection("authentication_history", history)

	// isAdmin is passed as false regardless of role: this is the account's own
	// data, not an administrative dump. An admin exporting their personal data
	// should not receive every other tenant's crawl records inside it.
	w.Write([]byte(`"crawl_records":[`))
	firstNode := true
	if err := d.cfg.DB.ExportNodes(r.Context(), uid, false, func(n database.Node) error {
		if !firstNode {
			if _, err := w.Write([]byte(",")); err != nil {
				return err
			}
		}
		firstNode = false
		return enc.Encode(n)
	}); err != nil {
		slog.ErrorContext(r.Context(), "export_failed", "format", "privacy", "kind", "nodes", "err", err)
	}

	w.Write([]byte(`],"links":[`))
	firstEdge := true
	if err := d.cfg.DB.ExportEdges(r.Context(), uid, func(e database.Edge) error {
		if !firstEdge {
			if _, err := w.Write([]byte(",")); err != nil {
				return err
			}
		}
		firstEdge = false
		return enc.Encode(e)
	}); err != nil {
		slog.ErrorContext(r.Context(), "export_failed", "format", "privacy", "kind", "edges", "err", err)
	}
	w.Write([]byte("]}"))
}

// handlePrivacyPurge deletes one category of the account's stored data.
func (d *deps) handlePrivacyPurge(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	var req struct {
		Scope    string `json:"scope"`
		Password string `json:"password"`
		Code     string `json:"code,omitempty"`
	}
	if !decodeJSONBody(w, r, 1024, &req) {
		return
	}
	// Reassign to a literal so the scope that reaches the log and the switch
	// below is a known value rather than a reference to caller input.
	var scope string
	switch req.Scope {
	case "crawl_history":
		scope = "crawl_history"
	case "page_content":
		scope = "page_content"
	case "activity_log":
		scope = "activity_log"
	default:
		WriteJSONError(w, http.StatusBadRequest, "Unknown data category")
		return
	}

	user := d.requireReauth(w, r, "purge", req.Password, req.Code)
	if user == nil {
		return
	}

	var affected int64
	var err error
	var message string
	switch scope {
	case "crawl_history":
		affected, err = d.cfg.DB.DeleteCrawlHistory(uid)
		message = "Crawl history deleted."
	case "page_content":
		affected, err = d.cfg.DB.PurgeStoredContent(uid)
		message = "Stored page content deleted. The records of which sites you crawled are kept."
	case "activity_log":
		affected, err = d.cfg.DB.PurgeAuthAudit(auditEmailReference(user.Email))
		message = "Account activity history deleted."
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "privacy_purge_failed", "uid", uid, "scope", scope, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	slog.InfoContext(r.Context(), "privacy_purge", "uid", uid, "scope", scope, "rows", affected)
	writeNoStoreJSON(w, http.StatusOK, map[string]any{
		"scope":   scope,
		"deleted": affected,
		"message": message,
	})
}

// handleAccountDeleteRequest schedules the account for erasure.
//
// The confirmation email is not a courtesy. Re-authentication proves the caller
// holds the credentials, but credentials are exactly what gets stolen — the mail
// reaches the address the account is registered at, which an attacker operating
// from a hijacked session does not control, and it names the deadline for
// undoing this. That combination is what makes the grace period protective
// rather than decorative.
func (d *deps) handleAccountDeleteRequest(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code,omitempty"`
	}
	if !decodeJSONBody(w, r, 1024, &req) {
		return
	}
	user := d.requireReauth(w, r, "account_delete", req.Password, req.Code)
	if user == nil {
		return
	}

	scheduled, err := d.cfg.DB.RequestAccountDeletion(uid, d.cfg.DeletionGrace)
	if err != nil {
		if errors.Is(err, database.ErrDeletionPending) {
			WriteJSONError(w, http.StatusConflict, "This account is already scheduled for deletion.")
			return
		}
		slog.ErrorContext(r.Context(), "account_delete_request_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	d.logAuthEvent("account_delete_requested", user.Email, ClientIP(r))
	slog.InfoContext(r.Context(), "account_delete_requested", "uid", uid, "scheduled_for", scheduled.Format(time.RFC3339))

	recipient := user.Email
	go func() {
		if err := email.SendAccountDeletionScheduledEmail(recipient, scheduled); err != nil {
			slog.Error("account_delete_email_failed", "err", err)
		}
	}()

	writeNoStoreJSON(w, http.StatusOK, map[string]any{
		"deletion_scheduled_for": scheduled.UTC().Format(time.RFC3339),
		"message":                "Your account is scheduled for deletion. You can cancel any time before then by signing in.",
	})
}

// handleAccountDeleteCancel calls off a pending deletion.
func (d *deps) handleAccountDeleteCancel(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	cancelled, err := d.cfg.DB.CancelAccountDeletion(uid)
	if err != nil {
		slog.ErrorContext(r.Context(), "account_delete_cancel_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if !cancelled {
		WriteJSONError(w, http.StatusBadRequest, "No deletion is scheduled for this account.")
		return
	}
	d.logAuthEvent("account_delete_cancelled", GetDBEmail(r), ClientIP(r))
	slog.InfoContext(r.Context(), "account_delete_cancelled", "uid", uid)

	recipient := GetDBEmail(r)
	go func() {
		if err := email.SendAccountDeletionCancelledEmail(recipient); err != nil {
			slog.Error("account_delete_cancel_email_failed", "err", err)
		}
	}()

	writeNoStoreJSON(w, http.StatusOK, map[string]string{
		"message": "Account deletion cancelled. Nothing was removed.",
	})
}
