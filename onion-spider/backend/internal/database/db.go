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
		processing_status VARCHAR(20) DEFAULT 'pending', -- pending, crawling, completed, failed
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

// Node reprezinta un site onion stocat in DB
type Node struct {
	ID               int    `json:"id"`
	URL              string `json:"url"`
	Title            string `json:"title"`
	StatusCode       int    `json:"status_code"`
	ServerHeader     string `json:"server_header"`
	ProcessingStatus string `json:"processing_status"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// SaveNode salveaza sau actualizeaza informatiile despre un site onion
func (db *DB) SaveNode(url, title, server string, statusCode int, status string) error {
	query := `
	INSERT INTO nodes (url, title, status_code, server_header, processing_status, last_crawled_at)
	VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP)
	ON CONFLICT (url) DO UPDATE SET
		title = EXCLUDED.title,
		status_code = EXCLUDED.status_code,
		server_header = EXCLUDED.server_header,
		processing_status = EXCLUDED.processing_status,
		last_crawled_at = CURRENT_TIMESTAMP;
	`
	_, err := db.Conn.Exec(query, url, title, statusCode, server, status)
	return err
}

// SaveEdge creeaza o legatura intre doua site-uri onion
func (db *DB) SaveEdge(source, target string) error {
	// Ne asiguram ca si target-ul exista in tabela nodes (ca pending) daca nu exista deja
	_, _ = db.Conn.Exec("INSERT INTO nodes (url, processing_status) VALUES ($1, 'pending') ON CONFLICT (url) DO NOTHING", target)

	query := `
	INSERT INTO edges (source_url, target_url)
	VALUES ($1, $2)
	ON CONFLICT (source_url, target_url) DO NOTHING;
	`
	_, err := db.Conn.Exec(query, source, target)
	return err
}

// GetNextPendingNode extrage urmatorul URL care trebuie scanat
func (db *DB) GetNextPendingNode() (string, error) {
	var url string
	query := `
		UPDATE nodes 
		SET processing_status = 'crawling' 
		WHERE url = (
			SELECT url FROM nodes 
			WHERE processing_status = 'pending' 
			ORDER BY discovered_at ASC 
			LIMIT 1 
			FOR UPDATE SKIP LOCKED
		) 
		RETURNING url;
	`
	err := db.Conn.QueryRow(query).Scan(&url)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return url, err
}

func (db *DB) GetNodes() ([]Node, error) {
	rows, err := db.Conn.Query("SELECT id, url, COALESCE(title, ''), COALESCE(status_code, 0), COALESCE(server_header, ''), processing_status FROM nodes ORDER BY discovered_at DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.URL, &n.Title, &n.StatusCode, &n.ServerHeader, &n.ProcessingStatus); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

func (db *DB) GetEdges() ([]Edge, error) {
	rows, err := db.Conn.Query("SELECT source_url, target_url FROM edges LIMIT 500")
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
