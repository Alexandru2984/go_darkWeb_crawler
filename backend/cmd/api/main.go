package main

import (
	"context"
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
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
	l := &crawlLimiter{buckets: make(map[string]*limitBucket), limit: limit, window: window}
	// Goroutine de cleanup — sterge bucket-urile expirate la fiecare 10 minute
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			l.mu.Lock()
			now := time.Now()
			for ip, b := range l.buckets {
				if now.After(b.resetAt) {
					delete(l.buckets, ip)
				}
			}
			l.mu.Unlock()
		}
	}()
	return l
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
	keyBytes := []byte(apiKey)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// ConstantTimeCompare previne timing attacks
			incoming := []byte(r.Header.Get("X-API-Key"))
			if subtle.ConstantTimeCompare(incoming, keyBytes) != 1 {
				writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
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
	if page > 10000 {
		page = 10000
	}
	if limit <= 0 {
		limit = 50
	}
	offset = (page - 1) * limit
	return
}

// writeJSONError trimite un raspuns de eroare consistent in format JSON
func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// clientIP extrage IP-ul clientului din request, fara portul asociat.
func clientIP(r *http.Request) string {
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		ip = r.RemoteAddr
	}
	if host, _, err := net.SplitHostPort(ip); err == nil {
		return host
	}
	return ip
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
		AllowedMethods: []string{"GET", "POST", "DELETE", "OPTIONS"},
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
	searchLimiter := newCrawlLimiter(60, time.Minute)

	dbConn, err := database.InitDB(dsn)
	if err != nil {
		log.Fatalf("Eroare critica la conectarea la DB: %v", err)
	}

	engine := crawler.NewEngine(dbConn, torProxy, workers, maxDepth)
	engine.Start()

	// exportSem previne exporturi simultane (max 1 la un moment dat)
	exportSem := make(chan struct{}, 1)

	r.Get("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var resp struct {
			Status        string `json:"status"`
			DBConnected   bool   `json:"db_connected"`
			NodesCrawled  int    `json:"nodes_crawled"`
			PendingNodes  int    `json:"pending_nodes"`
			FailedNodes   int    `json:"failed_nodes"`
			CrawlingNodes int    `json:"crawling_nodes"`
			BlockedNodes  int    `json:"blocked_nodes"`
			TotalEdges    int    `json:"total_edges"`
			ActiveWorkers int    `json:"active_workers"`
		}
		resp.Status = "online"
		resp.ActiveWorkers = workers
		stats, err := dbConn.GetStats()
		if err == nil {
			resp.DBConnected = true
			resp.NodesCrawled = stats.NodesCrawled
			resp.PendingNodes = stats.PendingNodes
			resp.FailedNodes = stats.FailedNodes
			resp.CrawlingNodes = stats.CrawlingNodes
			resp.BlockedNodes = stats.BlockedNodes
			resp.TotalEdges = stats.TotalEdges
		}
		json.NewEncoder(w).Encode(resp)
	})

	r.Get("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		limit, offset := parsePagination(r)
		nodes, err := dbConn.GetNodes(limit, offset)
		if err != nil {
			log.Printf("[ERROR] GET /api/nodes: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}
		json.NewEncoder(w).Encode(nodes)
	})

	r.Get("/api/node", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		nodeURL := r.URL.Query().Get("url")
		if nodeURL == "" {
			writeJSONError(w, http.StatusBadRequest, "Parametrul 'url' este obligatoriu")
			return
		}
		node, err := dbConn.GetNodeByURL(nodeURL)
		if err != nil {
			log.Printf("[ERROR] GET /api/node url=%s: %v", nodeURL, err)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}
		if node == nil {
			writeJSONError(w, http.StatusNotFound, "Nodul nu a fost gasit")
			return
		}
		json.NewEncoder(w).Encode(node)
	})

	r.Get("/api/edges", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		limit, offset := parsePagination(r)
		edges, err := dbConn.GetEdges(limit, offset)
		if err != nil {
			log.Printf("[ERROR] GET /api/edges: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}
		json.NewEncoder(w).Encode(edges)
	})

	r.Get("/api/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ip := clientIP(r)
		if !searchLimiter.allow(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "Rate limit depasit — max 60 cautari/minut")
			return
		}
		q := r.URL.Query().Get("q")
		if q == "" {
			writeJSONError(w, http.StatusBadRequest, "Parametrul 'q' este obligatoriu")
			return
		}
		if len(q) > 200 {
			writeJSONError(w, http.StatusBadRequest, "Query prea lung (max 200 caractere)")
			return
		}
		category := r.URL.Query().Get("category")
		nodes, err := dbConn.SearchNodes(q, category)
		if err != nil {
			log.Printf("[ERROR] GET /api/search q=%s: %v", q, err)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}
		json.NewEncoder(w).Encode(nodes)
	})

	r.Post("/api/crawl", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !limiter.allow(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "Prea multe cereri. Incearca din nou in cateva minute.")
			return
		}
		var req struct {
			URL string `json:"url"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Body invalid")
			return
		}
		if !isValidOnionURL(req.URL) {
			writeJSONError(w, http.StatusBadRequest, "URL invalid: trebuie sa fie un URL .onion valid (http/https)")
			return
		}
		log.Printf("[AUDIT] POST /api/crawl ip=%s url=%s", ip, req.URL)
		if err := engine.AddToQueue(req.URL); err != nil {
			if errors.Is(err, database.ErrBlacklisted) {
				writeJSONError(w, http.StatusForbidden, "Domeniu blocat")
				return
			}
			log.Printf("[ERROR] POST /api/crawl ip=%s url=%s: %v", ip, req.URL, err)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"message": "URL adaugat in coada de crawling"})
	})

	r.Post("/api/recrawl", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !limiter.allow(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "Prea multe cereri. Incearca din nou in cateva minute.")
			return
		}
		var req struct {
			URL string `json:"url"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Body invalid")
			return
		}
		if req.URL == "" {
			writeJSONError(w, http.StatusBadRequest, "Campul 'url' este obligatoriu")
			return
		}
		found, canRequeue, err := dbConn.RequeueForCrawl(req.URL)
		if err != nil {
			log.Printf("[ERROR] POST /api/recrawl url=%s: %v", req.URL, err)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}
		if !found {
			writeJSONError(w, http.StatusNotFound, "URL-ul nu exista in baza de date")
			return
		}
		if !canRequeue {
			writeJSONError(w, http.StatusConflict, "Nodul este deja in curs de crawling")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"message": "Nodul a fost pus in coada pentru re-crawling"})
	})

	// GET /api/queue — statistici coada + urmatoarele 10 URL-uri pending
	r.Get("/api/queue", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		summary, err := dbConn.GetQueueSummary()
		if err != nil {
			log.Printf("[ERROR] GET /api/queue: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}
		json.NewEncoder(w).Encode(summary)
	})

	// GET /api/blacklist — lista domeniilor blocate
	r.Get("/api/blacklist", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		domains, err := dbConn.GetBlacklist()
		if err != nil {
			log.Printf("[ERROR] GET /api/blacklist: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}
		if domains == nil {
			domains = []string{}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"domains": domains})
	})

	// POST /api/blacklist — adauga un domeniu in blacklist
	r.Post("/api/blacklist", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Domain string `json:"domain"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 512)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Body invalid")
			return
		}
		req.Domain = strings.TrimSpace(req.Domain)
		if req.Domain == "" {
			writeJSONError(w, http.StatusBadRequest, "Campul 'domain' este obligatoriu")
			return
		}
		// Acceptam doar domenii .onion
		if !strings.HasSuffix(req.Domain, ".onion") {
			writeJSONError(w, http.StatusBadRequest, "Doar domeniile .onion pot fi blocate")
			return
		}
		if err := dbConn.AddBlacklist(req.Domain); err != nil {
			log.Printf("[ERROR] POST /api/blacklist domain=%s: %v", req.Domain, err)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Domeniu blocat: %s", req.Domain)})
	})

	// GET /api/export?format=json|csv — export streaming al nodurilor crawlate
	// Folosim http.ResponseController pentru a extinde deadline-ul pe handler-ul de streaming.
	r.Get("/api/export", func(w http.ResponseWriter, r *http.Request) {
		// Permite doar un export simultan pentru a evita supraincarcarea DB
		select {
		case exportSem <- struct{}{}:
			defer func() { <-exportSem }()
		default:
			writeJSONError(w, http.StatusTooManyRequests, "Export in desfasurare — incearca din nou in cateva momente")
			return
		}

		format := r.URL.Query().Get("format")
		if format != "csv" {
			format = "json"
		}

		// Extinde deadline-ul pentru export (poate dura mai mult de 30s)
		rc := http.NewResponseController(w)
		rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))

		if format == "csv" {
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Content-Disposition", `attachment; filename="onion_spider_export.csv"`)
			csvWriter := csv.NewWriter(w)
			csvWriter.Write([]string{"id", "url", "title", "status_code", "server_header", "processing_status", "category", "last_crawled_at"})
			err := dbConn.ExportNodes(func(n database.Node) error {
				return csvWriter.Write([]string{
					strconv.Itoa(n.ID), n.URL, n.Title,
					strconv.Itoa(n.StatusCode), n.ServerHeader,
					n.ProcessingStatus, n.Category, n.LastCrawledAt,
				})
			})
			csvWriter.Flush()
			if err != nil {
				log.Printf("[EXPORT] Eroare la export CSV: %v", err)
			}
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("["))
			first := true
			err := dbConn.ExportNodes(func(n database.Node) error {
				b, err := json.Marshal(n)
				if err != nil {
					return err
				}
				if !first {
					w.Write([]byte(","))
				}
				first = false
				_, err = w.Write(b)
				return err
			})
			w.Write([]byte("]"))
			if err != nil {
				log.Printf("[EXPORT] Eroare la export JSON: %v", err)
			}
		}
	})

	// POST /api/crawl/bulk — adauga mai multe URL-uri in coada deodata (max 20)
	r.Post("/api/crawl/bulk", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !limiter.allow(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "Prea multe cereri. Incearca din nou in cateva minute.")
			return
		}
		var req struct {
			URLs []string `json:"urls"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 10*1024)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Body invalid")
			return
		}
		if len(req.URLs) == 0 || len(req.URLs) > 20 {
			writeJSONError(w, http.StatusBadRequest, "Trimite 1-20 URL-uri in campul 'urls'")
			return
		}
		var added, skipped int
		for _, u := range req.URLs {
			if !isValidOnionURL(u) {
				skipped++
				continue
			}
			log.Printf("[AUDIT] POST /api/crawl/bulk ip=%s url=%s", ip, u)
			if err := engine.AddToQueue(u); err != nil {
				skipped++
			} else {
				added++
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]int{"added": added, "skipped": skipped})
	})

	// DELETE /api/blacklist/{domain} — scoate un domeniu din blacklist
	r.Delete("/api/blacklist/{domain}", func(w http.ResponseWriter, r *http.Request) {
		domain := strings.TrimSpace(chi.URLParam(r, "domain"))
		if domain == "" || !strings.HasSuffix(domain, ".onion") {
			writeJSONError(w, http.StatusBadRequest, "Domeniu invalid: trebuie sa fie un domeniu .onion")
			return
		}
		found, err := dbConn.DeleteBlacklist(domain)
		if err != nil {
			log.Printf("[ERROR] DELETE /api/blacklist domain=%s: %v", domain, err)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}
		if !found {
			writeJSONError(w, http.StatusNotFound, "Domeniu negasit in blacklist")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Domeniu scos din blacklist: %s", domain)})
	})

	// GET /api/stats/timeline — noduri descoperite pe zi in ultimele 30 de zile
	r.Get("/api/stats/timeline", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		stats, err := dbConn.GetTimelineStats()
		if err != nil {
			log.Printf("[ERROR] GET /api/stats/timeline: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}
		if stats == nil {
			stats = []database.TimelineStat{}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"timeline": stats})
	})

	srv := &http.Server{
		Addr:         "127.0.0.1:" + port,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

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
	if rawURL == "" || len(rawURL) > 2048 {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		strings.HasSuffix(parsed.Host, ".onion")
}
