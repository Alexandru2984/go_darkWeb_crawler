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

// InitDB initializeaza conexiunea la PostgreSQL si ruleaza migrarile de schema
func InitDB(dsn string) (*DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("eroare la deschiderea bazei de date: %w", err)
	}

	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("eroare la conectarea fizica la db: %w", err)
	}

	log.Println("Conexiune la PostgreSQL reusita!")

	if err = migrate(db); err != nil {
		return nil, fmt.Errorf("eroare la migrarea bazei de date: %w", err)
	}

	return &DB{Conn: db}, nil
}

func migrate(db *sql.DB) error {
	// Cream tabelele de baza daca nu exista (instalare noua)
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS nodes (
		id              SERIAL PRIMARY KEY,
		url             TEXT UNIQUE NOT NULL,
		title           TEXT,
		status_code     INT,
		server_header   VARCHAR(100),
		metadata        JSONB,
		content         TEXT,
		retry_count     INT DEFAULT 0,
		next_crawl_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		processing_status VARCHAR(20) DEFAULT 'pending',
		depth           INT DEFAULT 0,
		search_vector   TSVECTOR,
		discovered_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		last_crawled_at TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS edges (
		source_url    TEXT REFERENCES nodes(url) ON DELETE CASCADE,
		target_url    TEXT,
		discovered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (source_url, target_url)
	);
	`)
	if err != nil {
		return err
	}

	// Migrari incrementale pentru instalatii existente (sunt idempotente)
	incremental := []string{
		`ALTER TABLE nodes ALTER COLUMN url TYPE TEXT`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS content TEXT`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS retry_count INT DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS next_crawl_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS search_vector TSVECTOR`,
		`ALTER TABLE edges ALTER COLUMN source_url TYPE TEXT`,
		`ALTER TABLE edges ALTER COLUMN target_url TYPE TEXT`,
		`UPDATE nodes SET processing_status = 'pending' WHERE processing_status = 'pending_v2'`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_search_vector ON nodes USING GIN(search_vector)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(processing_status, next_crawl_at)`,
		`CREATE OR REPLACE FUNCTION nodes_search_vector_update() RETURNS trigger AS $$
		 BEGIN
		   NEW.search_vector := to_tsvector('english',
		     COALESCE(NEW.title, '') || ' ' || COALESCE(NEW.content, ''));
		   RETURN NEW;
		 END;
		 $$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS nodes_search_vector_trigger ON nodes`,
		`CREATE TRIGGER nodes_search_vector_trigger
		   BEFORE INSERT OR UPDATE ON nodes
		   FOR EACH ROW EXECUTE FUNCTION nodes_search_vector_update()`,
	}
	for _, m := range incremental {
		if _, err := db.Exec(m); err != nil {
			log.Printf("⚠️ Migrare ignorata (probabil deja aplicata): %v", err)
		}
	}

	return nil
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

// SaveNode salveaza sau actualizeaza informatiile despre un site onion dupa crawling
func (db *DB) SaveNode(url, title, server string, statusCode int, status string, metadata string, content string) error {
	if metadata == "" {
		metadata = "{}"
	}
	_, err := db.Conn.Exec(`
	INSERT INTO nodes (url, title, status_code, server_header, processing_status, metadata, content, last_crawled_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, CURRENT_TIMESTAMP)
	ON CONFLICT (url) DO UPDATE SET
		title             = EXCLUDED.title,
		status_code       = EXCLUDED.status_code,
		server_header     = EXCLUDED.server_header,
		processing_status = EXCLUDED.processing_status,
		metadata          = EXCLUDED.metadata,
		content           = EXCLUDED.content,
		last_crawled_at   = CURRENT_TIMESTAMP;
	`, url, title, statusCode, server, status, metadata, content)
	return err
}

// EnqueueURL adauga un URL in coada de crawling fara sa suprascrie date existente
func (db *DB) EnqueueURL(rawURL string, depth int) error {
	_, err := db.Conn.Exec(
		`INSERT INTO nodes (url, processing_status, depth) VALUES ($1, 'pending', $2) ON CONFLICT (url) DO NOTHING`,
		rawURL, depth,
	)
	return err
}

// SearchNodes efectueaza o cautare Full-Text pe titlu si continut folosind indexul GIN
func (db *DB) SearchNodes(searchQuery string) ([]Node, error) {
	rows, err := db.Conn.Query(`
		SELECT id, url, COALESCE(title, ''), COALESCE(status_code, 0), COALESCE(server_header, ''), processing_status
		FROM nodes
		WHERE search_vector @@ plainto_tsquery('english', $1)
		ORDER BY ts_rank(search_vector, plainto_tsquery('english', $1)) DESC
		LIMIT 50
	`, searchQuery)
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
	return nodes, rows.Err()
}

// SaveEdge creeaza o legatura intre doua site-uri si adauga target-ul in coada daca e nou
func (db *DB) SaveEdge(source, target string, targetDepth int) error {
	_, _ = db.Conn.Exec(
		`INSERT INTO nodes (url, processing_status, depth) VALUES ($1, 'pending', $2) ON CONFLICT (url) DO NOTHING`,
		target, targetDepth,
	)
	_, err := db.Conn.Exec(`
	INSERT INTO edges (source_url, target_url)
	VALUES ($1, $2)
	ON CONFLICT (source_url, target_url) DO NOTHING
	`, source, target)
	return err
}

// GetNextPendingNode extrage atomic urmatorul URL care trebuie scanat
func (db *DB) GetNextPendingNode() (string, int, error) {
	var nodeURL string
	var depth int
	err := db.Conn.QueryRow(`
		UPDATE nodes
		SET processing_status = 'crawling'
		WHERE url = (
			SELECT url FROM nodes
			WHERE processing_status = 'pending'
			  AND next_crawl_at <= CURRENT_TIMESTAMP
			ORDER BY next_crawl_at ASC, discovered_at ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING url, depth
	`).Scan(&nodeURL, &depth)
	if err == sql.ErrNoRows {
		return "", 0, nil
	}
	return nodeURL, depth, err
}

// FailNodeWithRetry inregistreaza un esec si programeaza o reincercare automata
func (db *DB) FailNodeWithRetry(nodeURL string) error {
	_, err := db.Conn.Exec(`
		UPDATE nodes
		SET retry_count       = retry_count + 1,
		    processing_status = CASE WHEN retry_count >= 2 THEN 'failed' ELSE 'pending' END,
		    next_crawl_at     = CURRENT_TIMESTAMP + (INTERVAL '15 minutes' * (retry_count + 1))
		WHERE url = $1
	`, nodeURL)
	return err
}

func (db *DB) GetNodes() ([]Node, error) {
	rows, err := db.Conn.Query(`
		SELECT id, url, COALESCE(title, ''), COALESCE(status_code, 0), COALESCE(server_header, ''), processing_status
		FROM nodes ORDER BY discovered_at DESC LIMIT 100`)
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
	return nodes, rows.Err()
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
	return edges, rows.Err()
}
