package api

import (
	"net/http"
	"sync"
	"time"

	"onion-spider/internal/email"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

// New builds the HTTP handler that exposes the entire API surface. It owns
// rate limiters and export-concurrency semaphores but does NOT start any
// goroutine background workers tied to the application lifecycle (engine
// start, sweeper, audit retention) — those remain in main().
func New(cfg Config) http.Handler {
	if cfg.SendInvitation == nil {
		cfg.SendInvitation = email.SendInvitationEmail
	}
	// A zero grace period would delete an account the moment it is requested,
	// removing the window in which a hijacked session's deletion can be undone.
	// Treat "unset" as the default rather than as "immediately": a caller that
	// forgot to configure this should get the safe behaviour, not the one that
	// destroys data soonest.
	if cfg.DeletionGrace <= 0 {
		cfg.DeletionGrace = 7 * 24 * time.Hour
	}

	d := &deps{
		cfg: cfg,

		crawlLim:       NewCrawlLimiter(20, time.Minute),
		searchLim:      NewCrawlLimiter(60, time.Minute),
		savedSearchLim: NewCrawlLimiter(60, time.Minute),
		collectionLim:  NewCrawlLimiter(60, time.Minute),
		bookmarkLim:    NewCrawlLimiter(60, time.Minute),
		queueLim:       NewCrawlLimiter(60, time.Minute),

		// Ten re-authentications an hour is far more than anyone deleting their
		// own data needs, and far fewer than online password guessing requires.
		sensitiveLim:  NewCrawlLimiter(10, time.Hour),
		passwordSlots: make(chan struct{}, 4),

		// Onion limits are aggregates over the whole front door, not per
		// visitor, so they are sized well above the clearnet per-address limit.
		// Registration is disabled on the onion vhost (it returns 404 there), so
		// its onion budget only has to cover a misrouted request.
		loginLim:    NewAnonLimiter(5, 100, time.Minute),
		registerLim: NewAnonLimiter(3, 3, time.Hour),
		verifyLim:   NewAnonLimiter(10, 120, time.Minute),
		resetLim:    NewAnonLimiter(5, 60, time.Hour),

		exportGlobalSem: make(chan struct{}, 4),
		exportPerUser:   &sync.Map{},
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(NoStore)
	r.Use(TrustedRealIP)
	r.Use(MetricsMiddleware)
	r.Use(SafeLogger())
	r.Use(middleware.Recoverer)
	r.Use(JWTMiddleware)
	// Must sit after JWTMiddleware: it decides whether to enforce based on
	// whether the credentials came from a cookie.
	r.Use(CSRFProtect)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: cfg.CORSOrigins,
		AllowedMethods: []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Content-Type", "Authorization", csrfHeader},
		ExposedHeaders: []string{"X-Has-More"},
		MaxAge:         300,
	}))

	// Public UI capability. Account and infrastructure configuration stay private.
	r.Get("/api/config", func(w http.ResponseWriter, r *http.Request) {
		writeNoStoreJSON(w, http.StatusOK, map[string]bool{
			"registration_open": cfg.AllowRegistration,
		})
	})

	// Public: auth endpoints (rate-limited, audit-logged).
	r.Post("/api/auth/register", d.handleRegister)
	r.Post("/api/auth/login", d.handleLogin)
	r.Get("/api/auth/verify", d.handleVerifyGET)
	r.Post("/api/auth/verify", d.handleVerifyPOST)
	r.Post("/api/auth/forgot", d.handleForgotPassword)
	r.Post("/api/auth/reset", d.handleResetPassword)
	r.Post("/api/auth/activate", d.handleActivateAccount)

	// Public-ish: shows only that the server is running; private counts gated
	// behind LoadDBRole so admin sees globals, others see only their own.
	r.With(LoadDBRole(cfg.DB, cfg.RequireAdminMFA)).Get("/api/status", d.handleStatus)

	// Authenticated: any logged-in user.
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth)
		r.Use(LoadDBRole(cfg.DB, cfg.RequireAdminMFA))

		r.Get("/api/auth/me", d.handleMe)
		r.Post("/api/auth/logout", d.handleLogout)
		r.Post("/api/auth/logout-all", d.handleLogoutAll)
		r.Post("/api/auth/password", d.handleChangePassword)

		// Second-factor management. Outside the admin group on purpose: an
		// admin who must enrol has to be able to reach these.
		r.Get("/api/auth/sessions", d.handleSessions)
		r.Delete("/api/auth/sessions/{id}", d.handleRevokeSession)

		r.Get("/api/auth/totp", d.handleTOTPStatus)
		r.Post("/api/auth/totp/setup", d.handleTOTPSetup)
		r.Post("/api/auth/totp/confirm", d.handleTOTPConfirm)
		r.Post("/api/auth/totp/disable", d.handleTOTPDisable)
		r.Post("/api/auth/totp/recovery", d.handleRegenerateRecoveryCodes)

		// Privacy centre. Like second-factor management, deliberately outside
		// the admin group: an account must always be able to see, export and
		// delete its own data, whatever else it is or is not allowed to do.
		r.Get("/api/privacy/settings", d.handlePrivacySettings)
		r.Post("/api/privacy/settings", d.handlePrivacySettingsUpdate)
		r.Get("/api/privacy/export", d.handlePrivacyExport)
		r.Post("/api/privacy/purge", d.handlePrivacyPurge)
		r.Post("/api/privacy/account/delete", d.handleAccountDeleteRequest)
		r.Post("/api/privacy/account/delete/cancel", d.handleAccountDeleteCancel)

		// Annotations and watches. All POST, including the reads: these carry a
		// .onion address, and a request line ends up in browser history, the
		// Referer header, and every proxy log on the way.
		r.Post("/api/annotations", d.handleAnnotation)
		r.Post("/api/annotations/tag", d.handleTag)
		r.Post("/api/annotations/note", d.handleNote)
		r.Get("/api/tags", d.handleTags)
		r.Post("/api/collections/add", d.handleCollectionAdd)
		r.Post("/api/collections/rename", d.handleCollectionRename)
		r.Post("/api/collections/delete", d.handleCollectionDelete)

		r.Post("/api/watch", d.handleWatch)
		r.Get("/api/watches", d.handleWatches)
		r.Get("/api/watch/events", d.handleWatchEvents)
		r.Post("/api/watch/events/query", d.handleWatchQuery)
		r.Post("/api/bookmarks", d.handleBookmark)
		r.Post("/api/watch/events/seen", d.handleWatchEventsSeen)

		r.Get("/api/nodes", d.handleNodes)
		r.Post("/api/node", d.handleNode)
		r.Get("/api/edges", d.handleEdges)
		r.Post("/api/search", d.handleSearch)
		r.Post("/api/browse", d.handleBrowse)

		// Preferences are private even for admins. Sensitive names and query
		// terms are carried only in JSON bodies, never in resource paths.
		r.Get("/api/saved-searches", d.handleSavedSearchList)
		r.Post("/api/saved-searches", d.handleSavedSearchCreate)
		r.Post("/api/saved-searches/{id}", d.handleSavedSearchRename)
		r.Delete("/api/saved-searches/{id}", d.handleSavedSearchDelete)

		r.Post("/api/crawl", d.handleCrawl)
		r.Post("/api/recrawl", d.handleRecrawl)
		r.Post("/api/crawl/bulk", d.handleCrawlBulk)
		r.Get("/api/queue", d.handleQueue)
		r.Post("/api/queue/pause", d.handleQueuePause)
		r.Post("/api/queue/jobs", d.handleQueueJobs)
		r.Post("/api/queue/action", d.handleQueueAction)
		r.Post("/api/crawl/bulk/preview", d.handleCrawlPreview)

		r.Get("/api/stats/timeline", d.handleTimeline)
		r.Get("/api/export", d.handleExport)
	})

	// Admin-only: account invitations and blacklist management.
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth)
		r.Use(LoadDBRole(cfg.DB, cfg.RequireAdminMFA))
		r.Use(RequireAdminDB)

		r.Get("/api/admin/users", d.handleAdminUsers)
		r.Post("/api/admin/users", d.handleAdminCreateUser)
		r.Post("/api/admin/users/{id}/invitation", d.handleAdminResendInvitation)

		r.Get("/api/blacklist", d.handleBlacklistList)
		r.Post("/api/blacklist", d.handleBlacklistAdd)
		r.Delete("/api/blacklist/{domain}", d.handleBlacklistDelete)
	})

	return r
}
