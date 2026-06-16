package api

import (
	"net/http"
	"strconv"
	"time"

	"onion-spider/internal/metrics"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// MetricsMiddleware records request count and latency per matched route. The
// chi route pattern (e.g. "/api/blacklist/{domain}") is read AFTER serving, so
// the label cardinality stays bounded — the raw path is never used as a label.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK // handler returned without an explicit WriteHeader
		}
		metrics.HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(status)).Inc()
		metrics.HTTPDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
	})
}
