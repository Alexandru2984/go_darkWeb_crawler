package api

import (
	"net/http"
	"sync"
	"time"

	"onion-spider/internal/crawler"
	"onion-spider/internal/database"
)

// Config is what the application passes to New() to build the HTTP handler.
type Config struct {
	DB     *database.DB
	Engine *crawler.Engine

	AllowRegistration bool
	AdminEmail        string

	// SendInvitation is injectable so tests never send real mail. New uses the
	// SMTP implementation when omitted; delivery failures remain visible.
	SendInvitation func(to, token string) error

	// RequireAdminMFA gates administrative endpoints on the admin having a
	// second factor enrolled. It does not gate ordinary use, and enrolment
	// itself is never gated, so turning this on cannot lock an admin out of
	// the account they need in order to comply with it.
	RequireAdminMFA bool
	Workers         int
	CORSOrigins     []string

	// DeletionGrace is how long a requested account deletion waits before the
	// sweeper acts on it. The delay is the only chance to reverse the one
	// operation in this application that destroys data outright, so a zero
	// value is treated as "use the default" rather than "delete immediately".
	DeletionGrace time.Duration
}

// deps bundles the shared state used by HTTP handlers. It is created by New().
type deps struct {
	cfg Config

	// Authenticated endpoints are charged to the account (see RequestKey).
	crawlLim       *CrawlLimiter
	searchLim      *CrawlLimiter
	savedSearchLim *CrawlLimiter

	// sensitiveLim covers the endpoints that re-check the password before
	// destroying data. They answer whether a password was correct, which makes
	// them a guessing oracle for anyone already holding a session; this is the
	// equivalent of the account lockout that guards the login path.
	sensitiveLim *CrawlLimiter

	// Password verification and hashing share a process-wide memory budget.
	passwordSlots chan struct{}

	// Pre-authentication endpoints have no account to charge, so they are
	// limited per address on clearnet and in aggregate on the onion path.
	loginLim    *AnonLimiter
	registerLim *AnonLimiter
	verifyLim   *AnonLimiter
	resetLim    *AnonLimiter

	// Export concurrency control: max 1 export per user (per-user semaphore in
	// the sync.Map) AND a global cap (exportGlobalSem) so we never OOM.
	exportGlobalSem chan struct{}
	exportPerUser   *sync.Map
}

// acquirePasswordSlot bounds concurrent memory-hard hashes across accounts.
func (d *deps) acquirePasswordSlot(w http.ResponseWriter) (release func(), ok bool) {
	select {
	case d.passwordSlots <- struct{}{}:
		return func() { <-d.passwordSlots }, true
	default:
		w.Header().Set("Retry-After", "1")
		WriteJSONError(w, http.StatusServiceUnavailable, "Authentication is busy. Try again shortly.")
		return nil, false
	}
}
