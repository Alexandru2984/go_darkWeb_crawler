package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"  info  ", slog.LevelInfo},
		{"debug", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"nonsense", slog.LevelInfo}, // unknown → default
	}
	for _, c := range cases {
		if got := parseLevel(c.in); got != c.want {
			t.Errorf("parseLevel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRequestIDRoundTrip(t *testing.T) {
	ctx := WithRequestID(context.Background(), "abc-123")
	if got := RequestIDFromContext(ctx); got != "abc-123" {
		t.Errorf("RequestIDFromContext = %q, want %q", got, "abc-123")
	}
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Errorf("empty context should return \"\", got %q", got)
	}
}

// TestContextHandlerEmitsRequestID verifies the wrapping ContextHandler
// surfaces req_id automatically when a log call uses a context that carries
// one. This is the load-bearing invariant for request correlation.
func TestContextHandlerEmitsRequestID(t *testing.T) {
	var buf bytes.Buffer
	os.Setenv("LOG_FORMAT", "json")
	defer os.Unsetenv("LOG_FORMAT")

	logger := New(&buf)
	ctx := WithRequestID(context.Background(), "req-42")
	logger.InfoContext(ctx, "test_event", "k", "v")

	var rec map[string]any
	if err := json.NewDecoder(strings.NewReader(buf.String())).Decode(&rec); err != nil {
		t.Fatalf("log line is not valid JSON: %v\nline: %s", err, buf.String())
	}
	if rec["req_id"] != "req-42" {
		t.Errorf("req_id = %v, want %q", rec["req_id"], "req-42")
	}
	if rec["msg"] != "test_event" {
		t.Errorf("msg = %v, want %q", rec["msg"], "test_event")
	}
	if rec["k"] != "v" {
		t.Errorf("k = %v, want %q", rec["k"], "v")
	}
}

// TestContextHandlerNoIDWhenAbsent — calls without a request_id in context
// must not emit an empty/spurious req_id attribute.
func TestContextHandlerNoIDWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	os.Setenv("LOG_FORMAT", "json")
	defer os.Unsetenv("LOG_FORMAT")

	logger := New(&buf)
	logger.InfoContext(context.Background(), "test_event")

	var rec map[string]any
	if err := json.NewDecoder(strings.NewReader(buf.String())).Decode(&rec); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if _, present := rec["req_id"]; present {
		t.Errorf("req_id should be absent on a no-ID context, got %v", rec["req_id"])
	}
}

func TestTextFormatRespected(t *testing.T) {
	var buf bytes.Buffer
	os.Setenv("LOG_FORMAT", "text")
	defer os.Unsetenv("LOG_FORMAT")

	logger := New(&buf)
	logger.Info("text_event", "k", "v")

	// TextHandler emits "key=value" pairs — not JSON.
	if json.Valid(buf.Bytes()) {
		t.Errorf("text format should not produce valid JSON, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "k=v") {
		t.Errorf("text output missing k=v, got: %s", buf.String())
	}
}

func TestPrivacyFilterPseudonymizesSensitiveAttributes(t *testing.T) {
	var buf bytes.Buffer
	t.Setenv("LOG_FORMAT", "json")

	logger := New(&buf)
	onionURL := "http://abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwxyz2345.onion/private?q=secret"
	logger.Info("privacy_test",
		"url", onionURL,
		"email", "person@example.com",
		"ip", "203.0.113.42",
		"q", "sensitive search",
	)

	line := buf.String()
	for _, forbidden := range []string{onionURL, "person@example.com", "203.0.113.42", "sensitive search"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("sensitive value %q reached the log: %s", forbidden, line)
		}
	}

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	for _, key := range []string{"url", "email", "ip", "q"} {
		got, _ := rec[key].(string)
		if !strings.HasPrefix(got, "ref:") {
			t.Errorf("%s = %q, want an ephemeral reference", key, got)
		}
	}
}

func TestPrivacyFilterRedactsSecretsEmbeddedInErrors(t *testing.T) {
	var buf bytes.Buffer
	t.Setenv("LOG_FORMAT", "json")

	logger := New(&buf)
	logger.Error("request_failed", "err", errors.New(
		`GET http://abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwxyz2345.onion/private?token=abc for person@example.com: password=hunter2`,
	))

	line := buf.String()
	for _, forbidden := range []string{"abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwxyz2345.onion", "person@example.com", "abc", "hunter2"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("secret %q reached the log: %s", forbidden, line)
		}
	}
	for _, marker := range []string{"[uri-redacted]", "[email-redacted]", "password=[redacted]"} {
		if !strings.Contains(line, marker) {
			t.Errorf("redaction marker %q missing from: %s", marker, line)
		}
	}
}
