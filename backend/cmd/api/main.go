package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"onion-spider/internal/crawler"
	"onion-spider/internal/database"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"
)

// crawlLimiter este un rate limiter per-IP cu fereastra fixa pentru /api/crawl
type crawlLimiter struct {
	mu      sync.Mutex
	buckets map[string]*limitBucket
	limit   int
	window  time.Duration
}

type limitBucket struct {
	count   int
	resetAt time.Time
}

func newCrawlLimiter(limit int, window time.Duration) *crawlLimiter {
	return &crawlLimiter{buckets: make(map[string]*limitBucket), limit: limit, window: window}
}

func (l *crawlLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok || now.After(b.resetAt) {
		l.buckets[ip] = &limitBucket{count: 1, resetAt: now.Add(l.window)}
		return true
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	return true
}

func apiKeyMiddleware(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-API-Key") != apiKey {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func parsePagination(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if limit <= 0 {
		limit = 50
	}
	offset = (page - 1) * limit
	return
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️ Nu am gasit un fisier .env, folosesc variabilele din sistem")
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("Eroare critica: Lipseste DATABASE_URL din variabilele de mediu")
	}

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

	// CORS_ORIGIN: origini permise pentru frontend (separat cu virgula daca sunt mai multe)
	corsOrigin := os.Getenv("CORS_ORIGIN")
	if corsOrigin == "" {
		corsOrigin = "http://localhost:5173"
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: strings.Split(corsOrigin, ","),
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Content-Type", "X-API-Key"},
		MaxAge:         300,
	}))

	// Daca API_KEY e setat in env, toate request-urile trebuie sa il includa
	if apiKey := os.Getenv("API_KEY"); apiKey != "" {
		r.Use(apiKeyMiddleware(apiKey))
		log.Println("🔒 Autentificare API key activata.")
	} else {
		log.Println("⚠️ API_KEY nu este setat — API-ul este public (recomandat doar pentru dezvoltare).")
	}

	limiter := newCrawlLimiter(20, time.Minute)

	dbConn, err := database.InitDB(dsn)
	if err != nil {
		log.Fatalf("Eroare critica la conectarea la DB: %v", err)
	}

	engine := crawler.NewEngine(dbConn, torProxy, workers, maxDepth)
	engine.Start()

	r.Get("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var resp struct {
			Status        string `json:"status"`
			DBConnected   bool   `json:"db_connected"`
			NodesCrawled  int    `json:"nodes_crawled"`
			PendingNodes  int    `json:"pending_nodes"`
			ActiveWorkers int    `json:"active_workers"`
		}
		resp.Status = "online"
		resp.ActiveWorkers = workers
		stats, err := dbConn.GetStats()
		if err == nil {
			resp.DBConnected = true
			resp.NodesCrawled = stats.NodesCrawled
			resp.PendingNodes = stats.PendingNodes
		}
		json.NewEncoder(w).Encode(resp)
	})

	r.Get("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		limit, offset := parsePagination(r)
		nodes, err := dbConn.GetNodes(limit, offset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(nodes)
	})

	r.Get("/api/edges", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		limit, offset := parsePagination(r)
		edges, err := dbConn.GetEdges(limit, offset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(edges)
	})

	r.Get("/api/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "Query is required", http.StatusBadRequest)
			return
		}
		if len(q) > 200 {
			http.Error(w, "Query prea lung (max 200 caractere)", http.StatusBadRequest)
			return
		}
		nodes, err := dbConn.SearchNodes(q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(nodes)
	})

	r.Post("/api/crawl", func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			ip = strings.SplitN(forwarded, ",", 2)[0]
		}
		if !limiter.allow(strings.TrimSpace(ip)) {
			http.Error(w, "Prea multe cereri. Incearca din nou in cateva minute.", http.StatusTooManyRequests)
			return
		}
		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if !isValidOnionURL(req.URL) {
			http.Error(w, "URL invalid: trebuie sa fie un URL .onion valid (http/https)", http.StatusBadRequest)
			return
		}
		if err := engine.AddToQueue(req.URL); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"message": "URL added to crawl queue"})
	})

	srv := &http.Server{Addr: ":" + port, Handler: r}

	go func() {
		log.Printf("=== [API] Serverul asculta pe portul %s ===", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Eroare la pornirea serverului: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Oprire graceful initiata...")
	engine.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Eroare la oprire server HTTP: %v", err)
	}
	log.Println("Server oprit.")
}

func isValidOnionURL(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		strings.HasSuffix(parsed.Host, ".onion")
}
