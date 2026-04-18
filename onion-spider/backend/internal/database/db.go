package database

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

type DB struct {
	Conn *sql.DB
}

// InitDB initializeaza conexiunea la PostgreSQL
func InitDB(dsn string) (*DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("eroare la deschiderea bazei de date: %w", err)
	}

	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("eroare la conectarea fizica la db: %w", err)
	}

	log.Println("Conexiune la PostgreSQL reusita!")

	// Crearea tabelului initial
	err = createTables(db)
	if err != nil {
		return nil, fmt.Errorf("eroare la crearea tabelelor: %w", err)
	}

	return &DB{Conn: db}, nil
}

func createTables(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS nodes (
		id SERIAL PRIMARY KEY,
		url VARCHAR(255) UNIQUE NOT NULL,
		title VARCHAR(255),
		status_code INT,
		server_header VARCHAR(100),
		metadata JSONB,
		discovered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		last_crawled_at TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS edges (
		source_url VARCHAR(255) REFERENCES nodes(url) ON DELETE CASCADE,
		target_url VARCHAR(255),
		discovered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (source_url, target_url)
	);
	`
	_, err := db.Exec(query)
	return err
}
