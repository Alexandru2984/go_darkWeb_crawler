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

	crawlLim    *CrawlLimiter
	searchLim   *CrawlLimiter
	loginLim    *CrawlLimiter
	registerLim *CrawlLimiter
	verifyLim   *CrawlLimiter

	// Export concurrency control: max 1 export per user (per-user semaphore in
	// the sync.Map) AND a global cap (exportGlobalSem) so we never OOM.
	exportGlobalSem chan struct{}
	exportPerUser   *sync.Map
}
