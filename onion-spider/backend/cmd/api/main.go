package main

import (
	"encoding/json"
	"log"
	"net/http"
	"onion-spider/internal/database"

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
	dbConnected := false
	if err != nil {
		log.Printf("Eroare la conectarea la DB: %v", err)
	} else {
		dbConnected = true
		_ = dbConn
	}

	r.Get("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "online",
			"active_workers": 0,
			"nodes_crawled": 0,
			"db_connected": dbConnected,
		})
	})

	log.Println("=== [API] Serverul asculta pe portul 8888 ===")
	if err := http.ListenAndServe(":8888", r); err != nil {
		log.Fatalf("Eroare la pornirea serverului: %v", err)
	}
}
