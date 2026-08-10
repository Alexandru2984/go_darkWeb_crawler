package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"

	"onion-spider/internal/auth"
	"onion-spider/internal/database"
	"onion-spider/internal/email"
)

// logScrub kept commented-out as a placeholder. slog with KV attributes
// serializes values as escaped JSON (or quoted text) so the CR/LF wrapper
// is no longer needed for log-injection defense. If a future Sprintf-style
// log statement reappears, replicate the helper here:
//
//	func logScrub(s string) string {
//	    s = strings.ReplaceAll(s, "\r", "")
//	    s = strings.ReplaceAll(s, "\n", "")
//	    return s
//	}

func (d *deps) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !d.cfg.AllowRegistration {
		WriteJSONError(w, http.StatusForbidden, "Registration is currently closed")
		return
	}
	ctx := r.Context()
	ip := ClientIP(r)
	if !d.registerLim.Allow(r) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many registrations from this IP. Please try again later.")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Invalid data")
		return
	}
	req.Email = database.NormalizeEmail(req.Email)
	if !EmailRegex.MatchString(req.Email) || len(req.Email) > 254 {
		WriteJSONError(w, http.StatusBadRequest, "Invalid email")
		return
	}
	if err := ValidatePassword(req.Password); err != nil {
		WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Rate-limit per recipient address — protects Gmail quota from abuse.
	// Max 3 register attempts per email per hour.
	if n, err := d.countRecentAuthEvents("register_ok", req.Email, 60); err == nil && n >= 3 {
		slog.InfoContext(ctx, "register_blocked", "ip", ip, "email", req.Email, "count", n)
		WriteJSONError(w, http.StatusTooManyRequests, "This email has already received too many verification emails. Try again in an hour.")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Password cannot be processed")
		return
	}
	token := auth.GenerateVerificationToken()

	role, err := d.cfg.DB.CreateRegisteredUser(req.Email, hash, token, d.cfg.AdminEmail)
	if err != nil {
		d.logAuthEvent("register_fail", req.Email, ip)
		if errors.Is(err, database.ErrEmailInUse) {
			slog.InfoContext(ctx, "register_conflict", "ip", ip, "email", req.Email)
			WriteJSONError(w, http.StatusBadRequest, "Error: email already in use or invalid data")
			return
		}
		slog.ErrorContext(ctx, "register_failed", "ip", ip, "email", req.Email, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}

	d.logAuthEvent("register_ok", req.Email, ip)
	slog.InfoContext(ctx, "register_ok", "ip", ip, "email", req.Email, "role", role)

	go func() {
		if err := email.SendVerificationEmail(req.Email, token); err != nil {
			slog.ErrorContext(ctx, "email_send_failed", "to", req.Email, "err", err)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "Account created! Please check your email."})
}

func (d *deps) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := ClientIP(r)
	if !d.loginLim.Allow(r) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many login attempts. Please try again in 1 minute.")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Mode     string `json:"mode,omitempty"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Invalid data")
		return
	}
	req.Email = database.NormalizeEmail(req.Email)
	if req.Email == "" || req.Password == "" {
		WriteJSONError(w, http.StatusBadRequest, "Email and password are required")
		return
	}
	if req.Mode == "" {
		req.Mode = "cookie"
	}
	if req.Mode != "cookie" && req.Mode != "bearer" {
		WriteJSONError(w, http.StatusBadRequest, "Mode must be 'cookie' or 'bearer'")
		return
	}

	// Account lockout: after 5 login_fail in 15min for the same email → 15min
	// timeout. Protects against distributed brute-force across multiple IPs.
	if n, err := d.countRecentAuthEvents("login_fail", req.Email, 15); err == nil && n >= 5 {
		// Run bcrypt anyway to keep timing constant (do not leak lockout state).
		auth.CheckAgainstDummy(req.Password)
		d.logAuthEvent("login_locked", req.Email, ip)
		slog.InfoContext(ctx, "login_locked", "ip", ip, "email", req.Email, "count", n)
		WriteJSONError(w, http.StatusTooManyRequests, "Account temporarily locked due to too many failed attempts. Wait 15 minutes.")
		return
	}

	user, err := d.cfg.DB.GetUserByEmail(req.Email)
	if err != nil {
		slog.ErrorContext(ctx, "get_user_by_email_failed", "err", err)
		// Run bcrypt to keep timing constant even on DB error.
		auth.CheckAgainstDummy(req.Password)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	// TIMING ATTACK MITIGATION: even on missing user, run bcrypt against a
	// dummy hash. Without this, the ~600ms time difference would let an attacker
	// enumerate registered emails.
	if user == nil {
		auth.CheckAgainstDummy(req.Password)
		d.logAuthEvent("login_fail", req.Email, ip)
		slog.InfoContext(ctx, "login_fail", "ip", ip, "email", req.Email, "reason", "unknown_user")
		WriteJSONError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		d.logAuthEvent("login_fail", req.Email, ip)
		slog.InfoContext(ctx, "login_fail", "ip", ip, "email", req.Email, "reason", "bad_password")
		WriteJSONError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	if !user.IsVerified {
		d.logAuthEvent("login_unverified", req.Email, ip)
		WriteJSONError(w, http.StatusForbidden, "Account is not yet verified")
		return
	}

	token, err := auth.GenerateToken(user.ID, user.TokenVersion)
	if err != nil {
		slog.ErrorContext(ctx, "jwt_generate_failed", "email", user.Email, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	d.logAuthEvent("login_ok", req.Email, ip)
	slog.InfoContext(ctx, "login_ok", "ip", ip, "email", user.Email, "role", user.Role)

	w.Header().Set("Content-Type", "application/json")
	response := map[string]string{"role": user.Role, "email": user.Email}
	if req.Mode == "bearer" {
		// Explicit API-client mode returns the credential but deliberately does not
		// set ambient cookies. The browser uses the default cookie mode, where the
		// bearer token never enters script-readable response data.
		response["token"] = token
	} else {
		setSessionCookies(w, r, token, newCSRFToken())
	}
	json.NewEncoder(w).Encode(response)
}

// handleMe reports the current session to the frontend. With the token in an
// HttpOnly cookie the page can no longer read its own claims, so identity has
// to come from the server. The role returned here is the live one from the DB
// (via LoadDBRole), not the possibly-stale value in the token.
func (d *deps) handleMe(w http.ResponseWriter, r *http.Request) {
	if GetUserID(r) == 0 {
		WriteJSONError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	role := "user"
	if IsAdmin(r) {
		role = "admin"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"email": GetDBEmail(r), "role": role})
}

// handleLogout clears this browser's session. It does not bump token_version —
// that is logout-all's job. Signing out on a shared machine should not kill the
// user's other sessions.
func (d *deps) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookies(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"message": "Signed out."})
}

// handleVerifyGET shows a confirmation page with a POST button — it does NOT
// consume the token. Protects against link-preview bots (Outlook/Gmail/Slack)
// that GET the URL and would auto-verify the account in the user's absence.
func (d *deps) handleVerifyGET(w http.ResponseWriter, r *http.Request) {
	if !d.verifyLim.Allow(r) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many attempts. Please try again in 1 minute.")
		return
	}
	token := r.URL.Query().Get("token")
	if len(token) < 16 || len(token) > 128 {
		WriteJSONError(w, http.StatusBadRequest, "Invalid token")
		return
	}
	// Don't render the raw token as text — embed in a hidden input only after
	// verifying it contains only HTML-safe chars (hex / URL-safe base64).
	if !TokenSafeRE.MatchString(token) {
		WriteJSONError(w, http.StatusBadRequest, "Invalid token")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Account confirmation</title>
<meta name="robots" content="noindex,nofollow"><meta name="referrer" content="no-referrer"></head>
<body style="font-family:sans-serif;max-width:480px;margin:4rem auto;text-align:center">
<h1>Confirm account activation</h1>
<p>Click the button below to complete email verification.</p>
<form method="POST" action="/api/auth/verify">
<input type="hidden" name="token" value="%s">
<button type="submit" style="padding:0.75rem 1.5rem;font-size:1rem;cursor:pointer">Confirm</button>
</form></body></html>`, html.EscapeString(token))
}

// handleVerifyPOST actually consumes the token (accepts JSON or form-encoded body).
func (d *deps) handleVerifyPOST(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := ClientIP(r)
	if !d.verifyLim.Allow(r) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many attempts. Please try again in 1 minute.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var token string
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var req struct {
			Token string `json:"token"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			WriteJSONError(w, http.StatusBadRequest, "Invalid body")
			return
		}
		token = req.Token
	} else {
		if err := r.ParseForm(); err != nil {
			WriteJSONError(w, http.StatusBadRequest, "Invalid form")
			return
		}
		token = r.PostFormValue("token")
	}
	if len(token) < 16 || len(token) > 128 || !TokenSafeRE.MatchString(token) {
		WriteJSONError(w, http.StatusBadRequest, "Invalid token")
		return
	}
	if err := d.cfg.DB.VerifyUser(token); err != nil {
		slog.InfoContext(ctx, "verify_fail", "ip", ip, "err", err)
		WriteJSONError(w, http.StatusBadRequest, "Token invalid, expired or already used")
		return
	}
	slog.InfoContext(ctx, "verify_ok", "ip", ip)
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "Account successfully verified. You can now log in."})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>Account verified</title></head><body style="font-family:sans-serif;max-width:480px;margin:4rem auto;text-align:center"><h1>Account successfully verified!</h1><p><a href="/">Back to login</a></p></body></html>`))
}

// handleForgotPassword issues a password-reset token and emails it. The
// response is ALWAYS a generic 200 — it never reveals whether the email exists
// (account-enumeration defense), mirroring the login timing strategy.
func (d *deps) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := ClientIP(r)
	if !d.resetLim.Allow(r) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many reset requests. Please try again later.")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Invalid data")
		return
	}
	req.Email = database.NormalizeEmail(req.Email)

	const genericMsg = "If an account exists for that address, a reset link has been sent."

	// Only act on syntactically-valid emails, but still return the generic
	// message for invalid ones so the response is indistinguishable.
	if EmailRegex.MatchString(req.Email) && len(req.Email) <= 254 {
		// Per-recipient cap: at most 3 reset emails per hour.
		if n, err := d.countRecentAuthEvents("reset_request", req.Email, 60); err == nil && n >= 3 {
			slog.InfoContext(ctx, "reset_blocked", "ip", ip, "email", req.Email, "count", n)
		} else {
			token := auth.GenerateVerificationToken()
			found, err := d.cfg.DB.SetResetToken(req.Email, token)
			if err != nil {
				slog.ErrorContext(ctx, "set_reset_token_failed", "err", err)
			} else if found {
				d.logAuthEvent("reset_request", req.Email, ip)
				slog.InfoContext(ctx, "reset_request", "ip", ip, "email", req.Email)
				go func() {
					if err := email.SendPasswordResetEmail(req.Email, token); err != nil {
						slog.ErrorContext(ctx, "reset_email_send_failed", "to", req.Email, "err", err)
					}
				}()
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": genericMsg})
}

// handleResetPassword consumes a reset token and sets a new password. Success
// bumps the user's token_version (in DB.ResetPassword), revoking all sessions.
func (d *deps) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := ClientIP(r)
	if !d.resetLim.Allow(r) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many attempts. Please try again later.")
		return
	}
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Invalid data")
		return
	}
	if len(req.Token) < 16 || len(req.Token) > 128 || !TokenSafeRE.MatchString(req.Token) {
		WriteJSONError(w, http.StatusBadRequest, "Invalid token")
		return
	}
	if err := ValidatePassword(req.Password); err != nil {
		WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Password cannot be processed")
		return
	}
	if err := d.cfg.DB.ResetPassword(req.Token, hash); err != nil {
		slog.InfoContext(ctx, "reset_fail", "ip", ip, "err", err)
		WriteJSONError(w, http.StatusBadRequest, "Token invalid, expired or already used")
		return
	}
	slog.InfoContext(ctx, "reset_ok", "ip", ip)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Password updated. Please log in with your new password."})
}

// handleLogoutAll revokes every session for the authenticated user by bumping
// token_version. Useful after a suspected token leak.
func (d *deps) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	if err := d.cfg.DB.BumpTokenVersion(uid); err != nil {
		slog.ErrorContext(r.Context(), "logout_all_failed", "uid", uid, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	slog.InfoContext(r.Context(), "logout_all", "uid", uid)
	// This browser's session is one of the ones just revoked; drop its cookies
	// too rather than leaving it to replay a token that now fails on every
	// request.
	clearSessionCookies(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"message": "All sessions have been logged out."})
}
