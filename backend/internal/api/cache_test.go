package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoStoreAppliesToSuccessAndErrorResponses(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})
			rr := httptest.NewRecorder()
			NoStore(next).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/test", nil))
			if got := rr.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}
