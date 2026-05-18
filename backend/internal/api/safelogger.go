package api

import (
	"log"
	"net/http"
	"strconv"
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
				// strconv.Quote on user-controlled values (uri can contain raw
				// query-string bytes; ClientIP comes from an nginx header) —
				// it is the explicit log-injection sanitizer recognized by
				// CodeQL's go/log-injection query (the %q format verb is
				// NOT, despite being equivalent at runtime).
				log.Printf("%s %s %d %dB %s from %s",
					r.Method, strconv.Quote(uri), ww.Status(), ww.BytesWritten(),
					time.Since(start).Round(time.Millisecond), strconv.Quote(ClientIP(r)))
			}()
			next.ServeHTTP(ww, r)
		})
	}
}
