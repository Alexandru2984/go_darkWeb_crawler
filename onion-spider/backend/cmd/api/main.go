package main

import (
	"encoding/json"
	"log"
	"net/http"
	"onion-spider/internal/crawler"
	"onion-spider/internal/database"
	"onion-spider/internal/proxy"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

func main() {
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

	dsn := "postgres://spider_user:>REDACTED@localhost:5432/onion_spider?sslmode=disable"
	dbConn, err := database.InitDB(dsn)
	if err != nil {
		log.Printf("Eroare critica la conectarea la DB: %v", err)
	}

	r.Get("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		dbConnected := dbConn != nil
		nodesCount := 0
		if dbConnected {
			_ = dbConn.Conn.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&nodesCount)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "online",
			"active_workers": 0,
			"nodes_crawled":  nodesCount,
			"db_connected":   dbConnected,
		})
	})

	r.Get("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if dbConn == nil {
			http.Error(w, "Database not connected", http.StatusInternalServerError)
			return
		}
		nodes, err := dbConn.GetNodes()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(nodes)
	})

	r.Get("/api/edges", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if dbConn == nil {
			http.Error(w, "Database not connected", http.StatusInternalServerError)
			return
		}
		edges, err := dbConn.GetEdges()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(edges)
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

		// Pornim crawler-ul asincron (fara sa blocam API-ul)
		go func(target string) {
			log.Printf("[API] Pornire crawl pentru: %s", target)
			client, err := proxy.NewTorClient("127.0.0.1:9050")
			if err != nil {
				log.Printf("Eroare initializare Tor: %v", err)
				return
			}

			result, err := crawler.ScrapePage(client, target)
			if err != nil {
				log.Printf("Eroare scanare %s: %v", target, err)
				return
			}

			if dbConn != nil {
				err = dbConn.SaveNode(target, result.Title, result.ServerHeader, 200)
				if err != nil {
					log.Printf("Eroare salvare nod %s: %v", target, err)
				}
				for _, link := range result.FoundOnions {
					err = dbConn.SaveEdge(target, link)
					if err != nil {
						log.Printf("Eroare salvare edge %s -> %s: %v", target, link, err)
					}
				}
			}
			log.Printf("[API] Crawl finalizat pentru: %s", target)
		}(req.URL)

		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"message": "Crawl started"})
	})

	log.Println("=== [API] Serverul asculta pe portul 8890 ===")
	if err := http.ListenAndServe(":8890", r); err != nil {
		log.Fatalf("Eroare la pornirea serverului: %v", err)
	}
}
