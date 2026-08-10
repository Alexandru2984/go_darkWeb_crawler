package api

import (
	"sync"

	"onion-spider/internal/crawler"
	"onion-spider/internal/database"
)

// Config is what the application passes to New() to build the HTTP handler.
type Config struct {
	DB     *database.DB
	Engine *crawler.Engine

	AllowRegistration bool
	AdminEmail        string
	Workers           int
	CORSOrigins       []string
}

// deps bundles the shared state used by HTTP handlers. It is created by New().
type deps struct {
	cfg Config

	// Authenticated endpoints are charged to the account (see RequestKey).
	crawlLim  *CrawlLimiter
	searchLim *CrawlLimiter

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
