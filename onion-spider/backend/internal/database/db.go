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

// SaveNode salveaza sau actualizeaza informatiile despre un site onion
func (db *DB) SaveNode(url, title, server string, statusCode int) error {
	query := `
	INSERT INTO nodes (url, title, status_code, server_header, last_crawled_at)
	VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
	ON CONFLICT (url) DO UPDATE SET
		title = EXCLUDED.title,
		status_code = EXCLUDED.status_code,
		server_header = EXCLUDED.server_header,
		last_crawled_at = CURRENT_TIMESTAMP;
	`
	_, err := db.Conn.Exec(query, url, title, statusCode, server)
	return err
}

// SaveEdge creeaza o legatura intre doua site-uri onion
func (db *DB) SaveEdge(source, target string) error {
	query := `
	INSERT INTO edges (source_url, target_url)
	VALUES ($1, $2)
	ON CONFLICT (source_url, target_url) DO NOTHING;
	`
	// Ne asiguram ca si target-ul exista in tabela nodes (pentru integritate referentiala daca e cazul)
	// dar momentan doar il inseram fara date daca nu exista
	_, _ = db.Conn.Exec("INSERT INTO nodes (url) VALUES ($1) ON CONFLICT (url) DO NOTHING", target)

	_, err := db.Conn.Exec(query, source, target)
	return err
}

// Node reprezinta un site onion stocat in DB
type Node struct {
	ID           int    `json:"id"`
	URL          string `json:"url"`
	Title        string `json:"title"`
	StatusCode   int    `json:"status_code"`
	ServerHeader string `json:"server_header"`
}

// Edge reprezinta o conexiune intre doua site-uri onion
type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// GetNodes returneaza toate nodurile din baza de date
func (db *DB) GetNodes() ([]Node, error) {
	rows, err := db.Conn.Query("SELECT id, url, COALESCE(title, ''), COALESCE(status_code, 0), COALESCE(server_header, '') FROM nodes")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.URL, &n.Title, &n.StatusCode, &n.ServerHeader); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// GetEdges returneaza toate legaturile din baza de date
func (db *DB) GetEdges() ([]Edge, error) {
	rows, err := db.Conn.Query("SELECT source_url, target_url FROM edges")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.Source, &e.Target); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, nil
}
