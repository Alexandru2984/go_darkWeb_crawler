package api

import (
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
	crawlLim  *CrawlLimiter
	searchLim *CrawlLimiter

	// sensitiveLim covers the endpoints that re-check the password before
	// destroying data. They answer whether a password was correct, which makes
	// them a guessing oracle for anyone already holding a session; this is the
	// equivalent of the account lockout that guards the login path.
	sensitiveLim *CrawlLimiter

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
