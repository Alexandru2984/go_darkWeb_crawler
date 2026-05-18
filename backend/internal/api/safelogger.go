package api

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// SafeLogger replaces the standard middleware.Logger: logs requests but OMITS
// the query string for sensitive paths (e.g. /api/auth/verify which receives
// the token in the URL — would otherwise end up in syslog/journald).
func SafeLogger(sensitivePaths ...string) func(http.Handler) http.Handler {
	sensitive := make(map[string]struct{}, len(sensitivePaths))
	for _, p := range sensitivePaths {
		sensitive[p] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				uri := r.URL.Path
				if r.URL.RawQuery != "" {
					if _, isSensitive := sensitive[r.URL.Path]; !isSensitive {
						uri = r.URL.Path + "?" + r.URL.RawQuery
					} else {
						uri = r.URL.Path + "?[REDACTED]"
					}
				}
				// %q on user-controlled values (uri may contain query-string
				// content; ClientIP comes from a header) — Go's quoted form
				// escapes CR/LF and is recognized by static analyzers as a
				// log-injection sanitizer.
				log.Printf("%s %q %d %dB %s from %q",
					r.Method, uri, ww.Status(), ww.BytesWritten(),
					time.Since(start).Round(time.Millisecond), ClientIP(r))
			}()
			next.ServeHTTP(ww, r)
		})
	}
}
