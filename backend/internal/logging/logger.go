// Package logging configures the application-wide slog.Logger. Output format
// (JSON / text) and level are env-driven so prod and dev can use the same
// binary with different observability story:
//
//   - LOG_FORMAT=json  → slog.JSONHandler (one record per line, parseable by
//     Loki / ELK / Datadog). This is the prod default.
//   - LOG_FORMAT=text  → slog.TextHandler (human-readable). Dev default.
//   - LOG_LEVEL=debug|info|warn|error  (default: info)
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// requestIDKey is the context key that middleware uses to stash the per-
// request correlation ID. It is intentionally unexported and uses a private
// struct{} type so other packages cannot accidentally collide with it.
type requestIDKey struct{}

// WithRequestID returns a new context that carries id as the per-request
// correlation ID. middleware.RequestID (chi) sets the same value on the
// response header; we read it from context so log lines include it.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFromContext returns the correlation ID set by WithRequestID, or
// "" if the context has none (e.g. background goroutines).
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// New constructs a logger configured from environment variables. It is safe
// to call early (before .env is loaded) — unknown LOG_FORMAT / LOG_LEVEL
// values fall back to sensible defaults rather than panicking.
//
// The returned logger always carries a req_id attribute pulled from context
// via a wrapping ContextHandler — callers should prefer the *Context family
// (InfoContext, ErrorContext, ...) so the ID flows automatically.
func New(out io.Writer) *slog.Logger {
	level := parseLevel(os.Getenv("LOG_LEVEL"))
	opts := &slog.HandlerOptions{Level: level}

	var base slog.Handler
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_FORMAT"))) {
	case "text":
		base = slog.NewTextHandler(out, opts)
	default:
		// JSON is the prod-friendly default.
		base = slog.NewJSONHandler(out, opts)
	}

	return slog.New(&contextHandler{Handler: base})
}

// NewDefault constructs the standard application logger writing to stderr and
// installs it as slog.Default so package-level slog.InfoContext / ErrorContext
// calls anywhere in the codebase pick it up.
func NewDefault() *slog.Logger {
	l := New(os.Stderr)
	slog.SetDefault(l)
	return l
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// contextHandler decorates every record with a req_id attribute drawn from
// the context, when present. Wrapping at the handler layer (rather than
// asking each call site to pass req_id explicitly) means the rest of the
// codebase can stay terse: `slog.InfoContext(ctx, "login_ok", "email", ...)`.
type contextHandler struct{ slog.Handler }

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestIDFromContext(ctx); id != "" {
		r.AddAttrs(slog.String("req_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithGroup(name)}
}
