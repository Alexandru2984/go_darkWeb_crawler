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
	"onion-spider/internal/proxy"

	"github.com/joho/godotenv"
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

	handler := api.New(api.Config{
		DB:                dbConn,
		Engine:            engine,
		AllowRegistration: os.Getenv("ALLOW_REGISTRATION") == "true",
		AdminEmail:        os.Getenv("ADMIN_EMAIL"),
		Workers:           workers,
		CORSOrigins:       api.SplitAndTrim(corsOrigin, ","),
	})

	srv := &http.Server{
		Addr:         "127.0.0.1:" + port,
		Handler:      handler,
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
