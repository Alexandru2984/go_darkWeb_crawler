package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"onion-spider/internal/api"
	"onion-spider/internal/auth"
	"onion-spider/internal/crawler"
	"onion-spider/internal/database"
	"onion-spider/internal/logging"
	"onion-spider/internal/metrics"
	"onion-spider/internal/proxy"

	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	// godotenv MUST run before logger init so LOG_LEVEL / LOG_FORMAT are
	// picked up from .env on dev boxes.
	_ = godotenv.Load() // missing .env is fine in prod (systemd EnvironmentFile)

	logger := logging.NewDefault()
	logger.Info("startup", "service", "onion-spider-api")

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Error("DATABASE_URL is missing")
		os.Exit(1)
	}

	// Force-load JWT_SECRET at startup — log.Fatal if missing or weak.
	auth.MustInitSecrets()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8900"
	}

	workers := 3
	if w, err := strconv.Atoi(os.Getenv("WORKERS")); err == nil && w > 0 {
		workers = w
	}

	maxDepth := 2
	if d, err := strconv.Atoi(os.Getenv("MAX_DEPTH")); err == nil && d > 0 {
		maxDepth = d
	}

	torProxy := os.Getenv("TOR_PROXY")
	if torProxy == "" {
		torProxy = "127.0.0.1:9050"
	}

	corsOrigin := os.Getenv("CORS_ORIGIN")
	if corsOrigin == "" {
		corsOrigin = "http://localhost:5173"
	}

	dbConn, err := database.InitDB(dsn)
	if err != nil {
		logger.Error("db connection failed", "err", err)
		os.Exit(1)
	}

	engine := crawler.NewEngine(dbConn, torProxy, workers, maxDepth)

	// Back-pressure on submissions. Without it one account can queue faster
	// than the workers drain, and the queue is shared storage: the cost lands
	// on every other tenant as unbounded table growth and crawl latency.
	engine.MaxPendingPerUser = 500
	if q, err := strconv.Atoi(os.Getenv("MAX_PENDING_PER_USER")); err == nil && q >= 0 {
		engine.MaxPendingPerUser = q
	}

	torCtrlAddr := os.Getenv("TOR_CONTROL_ADDR")
	if torCtrlAddr == "" {
		torCtrlAddr = "127.0.0.1:9051"
	}
	torCtrl := proxy.NewTorController(
		torCtrlAddr,
		os.Getenv("TOR_CONTROL_PASSWORD"),
		os.Getenv("TOR_CONTROL_COOKIE"),
		30*time.Second,
	)
	if _, err := torCtrl.RenewCircuit(); err != nil {
		logger.Warn("tor control port unavailable; circuit renewal disabled", "err", err)
	} else {
		engine.TorCtrl = torCtrl
		logger.Info("tor controller active")
	}

	engine.Start()

	// Sweeper: reset nodes stuck in 'crawling' (after a brutal worker crash).
	// Runs every minute, moves back to 'pending' nodes with crawl_started_at > 10min.
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			n, err := dbConn.ResetStuckCrawling(10 * time.Minute)
			if err != nil {
				logger.Error("sweeper failed", "op", "ResetStuckCrawling", "err", err)
				continue
			}
			if n > 0 {
				logger.Info("sweeper recovered stuck nodes", "count", n)
			}
		}
	}()

	// Revive sweeper: return nodes that have been 'failed' for a long time to the
	// queue. Without this, an outage lasting longer than the retry budget kills
	// the whole queue permanently — every node exhausts its retries, 'failed' is
	// terminal for the scheduler, and the crawler sits idle after the cause is
	// fixed. Batched so a large backlog trickles back instead of arriving at once.
	reviveAfter := 7 * 24 * time.Hour
	if v := os.Getenv("REVIVE_FAILED_AFTER_DAYS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			reviveAfter = time.Duration(d) * 24 * time.Hour
		}
	}
	reviveBatch := 500
	if v := os.Getenv("REVIVE_BATCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			reviveBatch = n
		}
	}
	go func() {
		run := func() {
			n, err := dbConn.ReviveFailedNodes(reviveAfter, reviveBatch)
			if err != nil {
				logger.Error("revive sweeper failed", "op", "ReviveFailedNodes", "err", err)
				return
			}
			if n > 0 {
				logger.Info("revived failed nodes", "count", n, "older_than", reviveAfter.String())
			}
		}
		run()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			run()
		}
	}()

	// auth_audit retention: delete entries older than 90 days (configurable).
	// Solves GDPR (PII = email) + unbounded table growth. Runs at startup + every 24h.
	auditRetention := 90 * 24 * time.Hour
	if v := os.Getenv("AUDIT_RETENTION_DAYS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			auditRetention = time.Duration(d) * 24 * time.Hour
		}
	}
	go func() {
		run := func() {
			n, err := dbConn.PurgeOldAuditLogs(auditRetention)
			if err != nil {
				logger.Error("retention purge failed", "op", "PurgeOldAuditLogs", "err", err)
				return
			}
			if n > 0 {
				logger.Info("retention purged auth_audit", "count", n, "older_than", auditRetention.String())
			}
		}
		run()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			run()
		}
	}()

	// Session sweeper: drop rows no token can name any more. Revoked sessions
	// are deliberately kept until their natural expiry, so the inventory still
	// explains what a leaked token could have done during its lifetime.
	go func() {
		run := func() {
			n, err := dbConn.DeleteExpiredSessions()
			if err != nil {
				logger.Error("session sweeper failed", "op", "DeleteExpiredSessions", "err", err)
				return
			}
			if n > 0 {
				logger.Info("session sweeper removed expired sessions", "count", n)
			}
		}
		run()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			run()
		}
	}()

	// How long a requested account deletion waits before the sweeper acts on it.
	// Seven days is long enough that someone who loses access over a weekend
	// still gets the chance to stop it. Zero is deliberately not accepted: an
	// immediate deletion would remove the only defence against a hijacked
	// session wiping the account before its owner ever hears about it.
	deletionGrace := 7 * 24 * time.Hour
	if v := os.Getenv("ACCOUNT_DELETION_GRACE_DAYS"); v != "" {
		if dgd, err := strconv.Atoi(v); err == nil && dgd > 0 {
			deletionGrace = time.Duration(dgd) * 24 * time.Hour
		}
	}

	// Retention sweeper: delete crawl records that have outlived the policy of
	// the account that owns them. Accounts default to keeping everything, so
	// this does nothing until someone opts in on the Privacy page.
	//
	// RETENTION_DRY_RUN reports what each pass would remove without removing
	// anything (see onionspider_retention_pending). An automatic destructive job
	// meeting a database that already has data in it should be watched for a
	// while before it is armed, and this is how.
	retentionBatch := 1000
	if v := os.Getenv("RETENTION_BATCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			retentionBatch = n
		}
	}
	retentionDryRun := os.Getenv("RETENTION_DRY_RUN") == "true"
	go func() {
		run := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			report, err := dbConn.ApplyRetention(ctx, retentionBatch, retentionDryRun)
			if err != nil {
				logger.Error("retention sweeper failed", "op", "ApplyRetention", "err", err)
				return
			}
			metrics.RetentionPending.WithLabelValues("nodes").Set(float64(report.NodesMatched))
			metrics.RetentionLastRun.SetToCurrentTime()
			if report.NodesDeleted > 0 {
				metrics.RetentionDeleted.WithLabelValues("nodes").Add(float64(report.NodesDeleted))
			}
			if report.NodesMatched > 0 {
				logger.Info("retention pass complete",
					"accounts", report.Accounts,
					"nodes_matched", report.NodesMatched,
					"nodes_deleted", report.NodesDeleted,
					"dry_run", report.DryRun)
			}
		}
		run()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			run()
		}
	}()

	// Account-deletion sweeper: erase accounts whose grace period has elapsed.
	//
	// Never dry-run. Retention is a policy the operator sets and can get wrong
	// on everyone's behalf; this acts only on accounts that asked for it, and
	// quietly not honouring that request is the failure mode to avoid.
	go func() {
		run := func() {
			due, err := dbConn.DueAccountDeletions(50)
			if err != nil {
				logger.Error("deletion sweeper failed", "op", "DueAccountDeletions", "err", err)
				return
			}
			for _, d := range due {
				// The audit table is keyed by a reference of the address rather
				// than by user_id, so the cascade cannot reach it; deriving the
				// same reference here is what lets the login history go with the
				// account it belongs to.
				ref := auth.AuditReference("email", database.NormalizeEmail(d.Email))
				if err := dbConn.DeleteAccount(d.UserID, ref); err != nil {
					logger.Error("account deletion failed", "uid", d.UserID, "err", err)
					continue
				}
				metrics.RetentionDeleted.WithLabelValues("accounts").Add(1)
				logger.Info("account deleted after grace period", "uid", d.UserID)
			}
		}
		run()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			run()
		}
	}()

	// Queue-depth gauge poller: refresh onionspider_queue_nodes every 30s so
	// Prometheus can chart pending/crawling/failed backlog over time.
	go func() {
		refresh := func() {
			counts, err := dbConn.GlobalStatusCounts()
			if err != nil {
				logger.Warn("queue gauge refresh failed", "err", err)
				return
			}
			for status, n := range counts {
				metrics.QueueNodes.WithLabelValues(status).Set(float64(n))
			}
		}
		refresh()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			refresh()
		}
	}()

	apiHandler := api.New(api.Config{
		DB:                dbConn,
		Engine:            engine,
		AllowRegistration: os.Getenv("ALLOW_REGISTRATION") == "true",
		AdminEmail:        os.Getenv("ADMIN_EMAIL"),
		// Administrative actions require a second factor unless explicitly
		// disabled. Defaulting to on means a fresh deployment is not quietly
		// less protected than an operator assumes; enrolment endpoints stay
		// reachable either way, so this cannot lock an admin out.
		RequireAdminMFA: os.Getenv("REQUIRE_ADMIN_MFA") != "false",
		DeletionGrace:   deletionGrace,
		Workers:         workers,
		CORSOrigins:     api.SplitAndTrim(corsOrigin, ","),
	})

	// Ops endpoints live on the root mux, OUTSIDE the chi middleware stack and
	// outside /api/. The prod nginx only proxies /api/, so /metrics, /healthz
	// and /readyz are reachable only on 127.0.0.1:<port> directly — never
	// exposed publicly. /healthz is liveness (process up); /readyz pings the DB.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := dbConn.Conn.PingContext(pingCtx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("db unavailable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", apiHandler)

	srv := &http.Server{
		Addr:         "127.0.0.1:" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("server listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server start failed", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("graceful shutdown initiated")
	engine.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("http server shutdown failed", "err", err)
	}
	logger.Info("server stopped")
}
