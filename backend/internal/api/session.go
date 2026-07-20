package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"

	"onion-spider/internal/auth"
)

const (
	// sessionCookie holds the JWT. HttpOnly, so script running on the page
	// cannot read it — an XSS can still act as the user while the page is
	// open, but it cannot exfiltrate a 4-hour bearer token for reuse
	// elsewhere, which is the difference that matters after the tab closes.
	sessionCookie = "os_session"

	// csrfCookie holds the double-submit token. Deliberately NOT HttpOnly:
	// the frontend has to read it to echo it back in a header. It is not a
	// credential on its own — it only proves the request came from a page on
	// this origin, since a cross-site attacker can cause the session cookie
	// to be sent but cannot read this one to reproduce the header.
	csrfCookie = "os_csrf"

	// csrfHeader is where the frontend echoes the csrfCookie value.
	csrfHeader = "X-CSRF-Token"
)

// isHTTPS reports whether the original client request used TLS. The Go server
// listens on plain HTTP behind nginx, so r.TLS is always nil here and the
// proxy-set header is the only signal. Getting this wrong in the safe
// direction (dev over plain HTTP) just means the Secure attribute is omitted
// locally; in production nginx always sets it to https.
func isHTTPS(r *http.Request) bool {
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

// newCSRFToken returns a random 256-bit token, hex encoded.
func newCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on Linux; if it somehow does, failing loudly
		// beats handing out a predictable CSRF token.
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// setSessionCookies issues the session and CSRF cookies after a successful login.
//
// SameSite=Strict rather than Lax: nothing in this app relies on arriving from
// another site with a live session. The two flows that do start off-site (email
// verification and password reset) carry their own single-use token in the URL
// and need no session cookie, so Strict costs nothing and removes cross-site
// request forgery as a category before the double-submit check is even reached.
func setSessionCookies(w http.ResponseWriter, r *http.Request, token, csrf string) {
	secure := isHTTPS(r)
	maxAge := int(auth.TokenTTL.Seconds())

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    csrf,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearSessionCookies expires both cookies. MaxAge<0 tells the browser to
// delete them; the attributes must otherwise match those used when setting, or
// the browser treats it as a different cookie and leaves the original in place.
func clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	secure := isHTTPS(r)
	for _, name := range []string{sessionCookie, csrfCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: name == sessionCookie,
			Secure:   secure,
			SameSite: http.SameSiteStrictMode,
		})
	}
}

// CSRFProtect rejects unsafe methods that were authenticated by cookie unless
// the request echoes the CSRF cookie back in a header.
//
// It applies ONLY to cookie-authenticated requests. A request carrying an
// explicit Authorization: Bearer header cannot be forged by another origin —
// the browser will not attach that header on its own — so API clients and
// scripts are unaffected and need no token.
func CSRFProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if AuthSource(r) != authSourceCookie {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(csrfCookie)
		if err != nil || cookie.Value == "" {
			WriteJSONError(w, http.StatusForbidden, "Missing CSRF token")
			return
		}
		sent := r.Header.Get(csrfHeader)
		// Constant-time compare: a byte-at-a-time comparison would leak the
		// expected token through timing, one byte per guess.
		if len(sent) != len(cookie.Value) ||
			subtle.ConstantTimeCompare([]byte(sent), []byte(cookie.Value)) != 1 {
			WriteJSONError(w, http.StatusForbidden, "Invalid CSRF token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
