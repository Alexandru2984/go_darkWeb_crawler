package api

import "net/http"

// NoStore prevents browsers, reverse proxies and CDNs from retaining API
// responses. Authenticated endpoints contain onion URLs and page content; even
// public auth responses may contain a session token or account-state signal.
// Applying this once at the router boundary is safer than relying on every new
// handler to remember the privacy requirement.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
