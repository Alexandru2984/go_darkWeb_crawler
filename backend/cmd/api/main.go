package main

import (
	"context"
	"log"
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
	"onion-spider/internal/proxy"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️  No .env file found, using system environment variables")
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("FATAL: DATABASE_URL is missing")
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
		log.Fatalf("FATAL: DB connection: %v", err)
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
		log.Printf("⚠️  TorController: control port unavailable, circuit renewal disabled: %v", err)
	} else {
		engine.TorCtrl = torCtrl
		log.Println("✅ TorController active")
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
				log.Printf("[sweeper] ResetStuckCrawling error: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("[sweeper] recovered %d nodes stuck in 'crawling'", n)
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
				log.Printf("[retention] PurgeOldAuditLogs error: %v", err)
				return
			}
			if n > 0 {
				log.Printf("[retention] deleted %d auth_audit entries older than %v", n, auditRetention)
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
		log.Printf("=== [API] Server listening on port %s ===", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error starting server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Graceful shutdown initiated...")
	engine.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Error shutting down HTTP server: %v", err)
	}
	log.Println("Server stopped.")
}
