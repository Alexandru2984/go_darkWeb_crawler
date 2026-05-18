package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// WriteJSONError sends a JSON-formatted error response with the given status code.
func WriteJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ParsePagination reads `page` and `limit` query params and returns DB limit/offset.
// Defaults: limit=50, page=1. Caps: limit<=200, page<=10000.
func ParsePagination(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		page = 10000
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset = (page - 1) * limit
	return
}

// ClientIP returns the IP portion of r.RemoteAddr. Since the backend binds to
// 127.0.0.1 and is fronted by nginx with chi's middleware.RealIP, the value
// here comes from the nginx-set X-Real-IP header — not spoofable from outside.
func ClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// SanitizeForLog escapes newlines so user-controlled values cannot inject
// forged log lines (URLs may contain %0a/%0d).
func SanitizeForLog(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

// SanitizeCSVField prevents CSV/XLSX formula injection by prefixing values that
// start with =, +, -, @, tab or carriage return with a single quote.
func SanitizeCSVField(s string) string {
	if len(s) > 0 {
		switch s[0] {
		case '=', '+', '-', '@', '\t', '\r':
			return "'" + s
		}
	}
	return s
}

// SplitAndTrim splits s by sep and trims spaces around each piece, dropping empty results.
func SplitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
