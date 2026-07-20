package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// withAuthSource builds a request carrying the context value JWTMiddleware
// would have set, without needing a real token.
func withAuthSource(method, source string) *http.Request {
	r := httptest.NewRequest(method, "/api/crawl", nil)
	if source != "" {
		r = r.WithContext(context.WithValue(r.Context(), authSourceContextKey, source))
	}
	return r
}

func okHandler() (http.Handler, *bool) {
	called := false
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	return h, &called
}

func TestCSRFProtect_BlocksCookieAuthWithoutToken(t *testing.T) {
	h, called := okHandler()
	r := withAuthSource(http.MethodPost, authSourceCookie)
	r.AddCookie(&http.Cookie{Name: csrfCookie, Value: "abc123"})
	// No X-CSRF-Token header — this is the forged cross-site request.
	w := httptest.NewRecorder()
	CSRFProtect(h).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if *called {
		t.Fatal("handler ran despite the missing CSRF token")
	}
}

func TestCSRFProtect_BlocksMismatchedToken(t *testing.T) {
	h, called := okHandler()
	r := withAuthSource(http.MethodPost, authSourceCookie)
	r.AddCookie(&http.Cookie{Name: csrfCookie, Value: "abc123"})
	r.Header.Set(csrfHeader, "def456")
	w := httptest.NewRecorder()
	CSRFProtect(h).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if *called {
		t.Fatal("handler ran despite a mismatched CSRF token")
	}
}

func TestCSRFProtect_AllowsMatchingToken(t *testing.T) {
	h, called := okHandler()
	r := withAuthSource(http.MethodPost, authSourceCookie)
	r.AddCookie(&http.Cookie{Name: csrfCookie, Value: "abc123"})
	r.Header.Set(csrfHeader, "abc123")
	w := httptest.NewRecorder()
	CSRFProtect(h).ServeHTTP(w, r)

	if w.Code != http.StatusOK || !*called {
		t.Fatalf("matching token was rejected: status = %d, called = %v", w.Code, *called)
	}
}

// A Bearer header cannot be attached by the browser on a cross-site request, so
// header-authenticated callers (scripts, the documented API scheme) must not be
// forced to carry a CSRF token.
func TestCSRFProtect_SkipsHeaderAuth(t *testing.T) {
	h, called := okHandler()
	r := withAuthSource(http.MethodPost, authSourceHeader)
	w := httptest.NewRecorder()
	CSRFProtect(h).ServeHTTP(w, r)

	if w.Code != http.StatusOK || !*called {
		t.Fatalf("bearer-authenticated request was blocked: status = %d", w.Code)
	}
}

// Unauthenticated POSTs (login, register, password reset) carry no session to
// forge, and must not be blocked before they reach their handler.
func TestCSRFProtect_SkipsUnauthenticated(t *testing.T) {
	h, called := okHandler()
	r := withAuthSource(http.MethodPost, "")
	w := httptest.NewRecorder()
	CSRFProtect(h).ServeHTTP(w, r)

	if w.Code != http.StatusOK || !*called {
		t.Fatalf("unauthenticated request was blocked: status = %d", w.Code)
	}
}

func TestCSRFProtect_SkipsSafeMethods(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		h, called := okHandler()
		r := withAuthSource(m, authSourceCookie)
		// Cookie present, header absent: a GET must still go through.
		r.AddCookie(&http.Cookie{Name: csrfCookie, Value: "abc123"})
		w := httptest.NewRecorder()
		CSRFProtect(h).ServeHTTP(w, r)

		if w.Code != http.StatusOK || !*called {
			t.Fatalf("%s was blocked: status = %d", m, w.Code)
		}
	}
}

func TestSetSessionCookies_Attributes(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	setSessionCookies(w, r, "jwt-value", "csrf-value")

	var session, csrf *http.Cookie
	for _, c := range w.Result().Cookies() {
		switch c.Name {
		case sessionCookie:
			session = c
		case csrfCookie:
			csrf = c
		}
	}
	if session == nil || csrf == nil {
		t.Fatal("both session and csrf cookies must be set")
	}
	// The whole point of the change: script on the page cannot read the token.
	if !session.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	// The frontend has to read this one to echo it into a header.
	if csrf.HttpOnly {
		t.Error("csrf cookie must NOT be HttpOnly")
	}
	for _, c := range []*http.Cookie{session, csrf} {
		if !c.Secure {
			t.Errorf("%s must be Secure when the request arrived over https", c.Name)
		}
		if c.SameSite != http.SameSiteStrictMode {
			t.Errorf("%s must be SameSite=Strict", c.Name)
		}
	}
}

// Over plain HTTP (local dev, no TLS-terminating proxy) the Secure attribute
// has to be omitted or the browser drops the cookie and login appears to
// silently fail.
func TestSetSessionCookies_NotSecureOverPlainHTTP(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	w := httptest.NewRecorder()
	setSessionCookies(w, r, "jwt-value", "csrf-value")

	for _, c := range w.Result().Cookies() {
		if c.Secure {
			t.Errorf("%s must not be Secure when the request was plain HTTP", c.Name)
		}
	}
}

func TestClearSessionCookies_ExpiresBoth(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	w := httptest.NewRecorder()
	clearSessionCookies(w, r)

	cookies := w.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("expected both cookies to be expired, got %d", len(cookies))
	}
	for _, c := range cookies {
		if c.MaxAge >= 0 {
			t.Errorf("%s MaxAge = %d, want negative so the browser deletes it", c.Name, c.MaxAge)
		}
	}
}

func TestNewCSRFToken_IsRandomAndSized(t *testing.T) {
	a, b := newCSRFToken(), newCSRFToken()
	if a == b {
		t.Fatal("two CSRF tokens must not be identical")
	}
	if len(a) != 64 { // 32 random bytes, hex encoded
		t.Fatalf("token length = %d, want 64", len(a))
	}
}
