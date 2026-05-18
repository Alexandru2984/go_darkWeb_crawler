package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

// New builds the HTTP handler that exposes the entire API surface. It owns
// rate limiters and export-concurrency semaphores but does NOT start any
// goroutine background workers tied to the application lifecycle (engine
// start, sweeper, audit retention) — those remain in main().
func New(cfg Config) http.Handler {
	d := &deps{
		cfg: cfg,

		crawlLim:    NewCrawlLimiter(20, time.Minute),
		searchLim:   NewCrawlLimiter(60, time.Minute),
		loginLim:    NewCrawlLimiter(5, time.Minute),
		registerLim: NewCrawlLimiter(3, time.Hour),
		verifyLim:   NewCrawlLimiter(10, time.Minute),

		exportGlobalSem: make(chan struct{}, 4),
		exportPerUser:   &sync.Map{},
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(SafeLogger("/api/auth/verify"))
	r.Use(middleware.Recoverer)
	r.Use(JWTMiddleware)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: cfg.CORSOrigins,
		AllowedMethods: []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Content-Type", "Authorization"},
		MaxAge:         300,
	}))

	// Public: auth endpoints (rate-limited, audit-logged).
	r.Post("/api/auth/register", d.handleRegister)
	r.Post("/api/auth/login", d.handleLogin)
	r.Get("/api/auth/verify", d.handleVerifyGET)
	r.Post("/api/auth/verify", d.handleVerifyPOST)

	// Public-ish: shows only that the server is running; private counts gated
	// behind LoadDBRole so admin sees globals, others see only their own.
	r.With(LoadDBRole(cfg.DB)).Get("/api/status", d.handleStatus)

	// Authenticated: any logged-in user.
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth)
		r.Use(LoadDBRole(cfg.DB))

		r.Get("/api/nodes", d.handleNodes)
		r.Get("/api/node", d.handleNode)
		r.Get("/api/edges", d.handleEdges)
		r.Get("/api/search", d.handleSearch)

		r.Post("/api/crawl", d.handleCrawl)
		r.Post("/api/recrawl", d.handleRecrawl)
		r.Post("/api/crawl/bulk", d.handleCrawlBulk)
		r.Get("/api/queue", d.handleQueue)

		r.Get("/api/stats/timeline", d.handleTimeline)
		r.Get("/api/export", d.handleExport)
	})

	// Admin-only: blacklist management.
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth)
		r.Use(LoadDBRole(cfg.DB))
		r.Use(RequireAdminDB)

		r.Get("/api/blacklist", d.handleBlacklistList)
		r.Post("/api/blacklist", d.handleBlacklistAdd)
		r.Delete("/api/blacklist/{domain}", d.handleBlacklistDelete)
	})

	return r
}
