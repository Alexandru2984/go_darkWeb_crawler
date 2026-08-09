package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appLog "onion-spider/internal/logging"

	"github.com/go-chi/chi/v5"
)

func TestSafeLoggerNeverPersistsRawPathQueryOrIP(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(appLog.New(&buf))
	t.Cleanup(func() { slog.SetDefault(old) })

	router := chi.NewRouter()
	router.Use(SafeLogger())
	router.Get("/api/node/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/node/private-value?q=secret", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	router.ServeHTTP(httptest.NewRecorder(), req)

	line := buf.String()
	for _, forbidden := range []string{"private-value", "q=secret", "203.0.113.9"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("request secret %q reached the log: %s", forbidden, line)
		}
	}
	if !strings.Contains(line, `"route":"/api/node/{id}"`) {
		t.Fatalf("bounded route pattern missing from log: %s", line)
	}
}
