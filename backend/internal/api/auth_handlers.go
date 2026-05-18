package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"onion-spider/internal/auth"
	"onion-spider/internal/database"
	"onion-spider/internal/email"
)

func (d *deps) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !d.cfg.AllowRegistration {
		WriteJSONError(w, http.StatusForbidden, "Registration is currently closed")
		return
	}
	ip := ClientIP(r)
	if !d.registerLim.Allow(ip) {
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
	if n, err := d.cfg.DB.CountRecentAuthEvents("register_ok", req.Email, 60); err == nil && n >= 3 {
		log.Printf("[AUDIT] register_blocked ip=%s email=%s count=%d", SanitizeForLog(ip), SanitizeForLog(req.Email), n)
		WriteJSONError(w, http.StatusTooManyRequests, "This email has already received too many verification emails. Try again in an hour.")
		return
	}

	// Default role: user. Admin bootstrap is controlled via ADMIN_EMAIL and is
	// only allowed if no admin exists yet in the system.
	role := "user"
	adminEmail := database.NormalizeEmail(d.cfg.AdminEmail)
	if adminEmail != "" && req.Email == adminEmail {
		hasAdmin, _ := d.cfg.DB.HasAnyAdmin()
		if !hasAdmin {
			role = "admin"
		}
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Password cannot be processed")
		return
	}
	token := auth.GenerateVerificationToken()

	if err := d.cfg.DB.CreateUser(req.Email, hash, role, token); err != nil {
		log.Printf("[AUDIT] register_fail ip=%s email=%s: %v", SanitizeForLog(ip), SanitizeForLog(req.Email), err)
		d.cfg.DB.LogAuthEvent("register_fail", req.Email, ip)
		WriteJSONError(w, http.StatusBadRequest, "Error: email already in use or invalid data")
		return
	}

	d.cfg.DB.LogAuthEvent("register_ok", req.Email, ip)
	log.Printf("[AUDIT] register_ok ip=%s email=%s role=%s", SanitizeForLog(ip), SanitizeForLog(req.Email), role)

	go func() {
		if err := email.SendVerificationEmail(req.Email, token); err != nil {
			log.Printf("[email] send error to %s: %v", SanitizeForLog(req.Email), err)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "Account created! Please check your email."})
}

func (d *deps) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)
	if !d.loginLim.Allow(ip) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many login attempts. Please try again in 1 minute.")
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
	if req.Email == "" || req.Password == "" {
		WriteJSONError(w, http.StatusBadRequest, "Email and password are required")
		return
	}

	// Account lockout: after 5 login_fail in 15min for the same email → 15min
	// timeout. Protects against distributed brute-force across multiple IPs.
	if n, err := d.cfg.DB.CountRecentAuthEvents("login_fail", req.Email, 15); err == nil && n >= 5 {
		// Run bcrypt anyway to keep timing constant (do not leak lockout state).
		auth.CheckAgainstDummy(req.Password)
		d.cfg.DB.LogAuthEvent("login_locked", req.Email, ip)
		log.Printf("[AUDIT] login_locked ip=%s email=%s count=%d", SanitizeForLog(ip), SanitizeForLog(req.Email), n)
		WriteJSONError(w, http.StatusTooManyRequests, "Account temporarily locked due to too many failed attempts. Wait 15 minutes.")
		return
	}

	user, err := d.cfg.DB.GetUserByEmail(req.Email)
	if err != nil {
		log.Printf("[ERROR] GetUserByEmail: %v", err)
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
		d.cfg.DB.LogAuthEvent("login_fail", req.Email, ip)
		log.Printf("[AUDIT] login_fail ip=%s email=%s reason=unknown_user", SanitizeForLog(ip), SanitizeForLog(req.Email))
		WriteJSONError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		d.cfg.DB.LogAuthEvent("login_fail", req.Email, ip)
		log.Printf("[AUDIT] login_fail ip=%s email=%s reason=bad_password", SanitizeForLog(ip), SanitizeForLog(req.Email))
		WriteJSONError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	if !user.IsVerified {
		d.cfg.DB.LogAuthEvent("login_unverified", req.Email, ip)
		WriteJSONError(w, http.StatusForbidden, "Account is not yet verified")
		return
	}

	token, err := auth.GenerateToken(user.ID, user.Email, user.Role)
	if err != nil {
		log.Printf("[ERROR] JWT generation for %s: %v", SanitizeForLog(user.Email), err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	d.cfg.DB.LogAuthEvent("login_ok", req.Email, ip)
	log.Printf("[AUDIT] login_ok ip=%s email=%s role=%s", SanitizeForLog(ip), SanitizeForLog(user.Email), user.Role)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": token, "role": user.Role, "email": user.Email})
}

// handleVerifyGET shows a confirmation page with a POST button — it does NOT
// consume the token. Protects against link-preview bots (Outlook/Gmail/Slack)
// that GET the URL and would auto-verify the account in the user's absence.
func (d *deps) handleVerifyGET(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)
	if !d.verifyLim.Allow(ip) {
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
</form></body></html>`, token)
}

// handleVerifyPOST actually consumes the token (accepts JSON or form-encoded body).
func (d *deps) handleVerifyPOST(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)
	if !d.verifyLim.Allow(ip) {
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
		log.Printf("[AUDIT] verify_fail ip=%s: %v", SanitizeForLog(ip), err)
		WriteJSONError(w, http.StatusBadRequest, "Token invalid, expired or already used")
		return
	}
	log.Printf("[AUDIT] verify_ok ip=%s", SanitizeForLog(ip))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>Account verified</title></head><body style="font-family:sans-serif;max-width:480px;margin:4rem auto;text-align:center"><h1>Account successfully verified!</h1><p><a href="/">Back to login</a></p></body></html>`))
}
