package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"onion-spider/internal/crawler"
	"onion-spider/internal/database"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Println("⚠️ Nu am gasit un fisier .env, folosesc variabilele din sistem")
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("Eroare critica: Lipseste DATABASE_URL din variabilele de mediu")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8900" // default fallback
	}

	workersStr := os.Getenv("WORKERS")
	workers := 3
	if w, err := strconv.Atoi(workersStr); err == nil {
		workers = w
	}

	torProxy := os.Getenv("TOR_PROXY")
	if torProxy == "" {
		torProxy = "127.0.0.1:9050"
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	dbConn, err := database.InitDB(dsn)
	if err != nil {
		log.Fatalf("Eroare critica la conectarea la DB: %v", err)
	}

	// Initializam Motorul de Crawling
	engine := crawler.NewEngine(dbConn, torProxy, workers)
	engine.Start()

	r.Get("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		
		var stats struct {
			Status        string `json:"status"`
			DBConnected   bool   `json:"db_connected"`
			NodesCrawled  int    `json:"nodes_crawled"`
			PendingNodes  int    `json:"pending_nodes"`
			ActiveWorkers int    `json:"active_workers"`
		}

		stats.Status = "online"
		stats.DBConnected = true
		stats.ActiveWorkers = workers
		
		_ = dbConn.Conn.QueryRow("SELECT COUNT(*) FROM nodes WHERE processing_status = 'completed'").Scan(&stats.NodesCrawled)
		_ = dbConn.Conn.QueryRow("SELECT COUNT(*) FROM nodes WHERE processing_status = 'pending_v2'").Scan(&stats.PendingNodes)

		json.NewEncoder(w).Encode(stats)
	})

	r.Get("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		nodes, err := dbConn.GetNodes()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(nodes)
	})

	r.Get("/api/edges", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		edges, err := dbConn.GetEdges()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(edges)
	})

	r.Get("/api/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if dbConn == nil {
			http.Error(w, "Database not connected", http.StatusInternalServerError)
			return
		}
		
		query := r.URL.Query().Get("q")
		if query == "" {
			http.Error(w, "Query is required", http.StatusBadRequest)
			return
		}

		nodes, err := dbConn.SearchNodes(query)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(nodes)
	})

	r.Post("/api/crawl", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.URL == "" {
			http.Error(w, "URL is required", http.StatusBadRequest)
			return
		}

		// Adaugam URL-ul in coada motorului (prin DB)
		err := engine.AddToQueue(req.URL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"message": "URL added to crawl queue"})
	})

	log.Printf("=== [API] Serverul asculta pe portul %s ===", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("Eroare la pornirea serverului: %v", err)
	}
}
