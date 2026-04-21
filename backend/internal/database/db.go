package database

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// ErrBlacklisted este returnat de EnqueueURL cand domeniul e pe blacklist.
var ErrBlacklisted = errors.New("domeniu blocat")

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

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	log.Println("Conexiune la PostgreSQL reusita!")

	if err = migrate(db); err != nil {
		return nil, fmt.Errorf("eroare la migrarea bazei de date: %w", err)
	}

	// Nodurile ramase in 'crawling' la un crash anterior nu vor fi niciodata reluate.
	// Le resetam la 'pending' la fiecare pornire.
	if _, err = db.Exec(`UPDATE nodes SET processing_status = 'pending' WHERE processing_status = 'crawling'`); err != nil {
		log.Printf("⚠️ Nu am putut reseta nodurile 'crawling': %v", err)
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
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS content_hash TEXT`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS re_crawl_interval_days INT DEFAULT 7`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS category VARCHAR(30) DEFAULT 'unknown'`,
		`ALTER TABLE edges ALTER COLUMN source_url TYPE TEXT`,
		`ALTER TABLE edges ALTER COLUMN target_url TYPE TEXT`,
		`UPDATE nodes SET processing_status = 'pending' WHERE processing_status = 'pending_v2'`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_search_vector ON nodes USING GIN(search_vector)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(processing_status, next_crawl_at)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_category ON nodes(category)`,
		// Tabel blacklist pentru domenii blocate
		`CREATE TABLE IF NOT EXISTS blacklist (
			domain   TEXT PRIMARY KEY,
			added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS host TEXT`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_host ON nodes(host)`,
		`UPDATE nodes SET host = (regexp_match(url, '^https?://([^/?#]+)'))[1] WHERE host IS NULL`,
		// Trigger conditional: recalculeaza search_vector doar cand titlul sau continutul se schimba
		`CREATE OR REPLACE FUNCTION nodes_search_vector_update() RETURNS trigger AS $$
		 BEGIN
		   IF TG_OP = 'INSERT' OR NEW.title IS DISTINCT FROM OLD.title OR NEW.content IS DISTINCT FROM OLD.content THEN
		     NEW.search_vector := to_tsvector('english',
		       COALESCE(NEW.title, '') || ' ' || COALESCE(NEW.content, ''));
		   END IF;
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
	Category         string `json:"category"`
	LastCrawledAt    string `json:"last_crawled_at,omitempty"`
}

// NodeDetail include si continutul complet — folosit pentru GET /api/node
type NodeDetail struct {
	Node
	Content      string `json:"content"`
	Metadata     string `json:"metadata"`
	ContentHash  string `json:"content_hash,omitempty"`
	DiscoveredAt string `json:"discovered_at"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// ContentHash calculeaza sha256(title + "|" + content) pentru detectia schimbarilor
func ContentHash(title, content string) string {
	h := sha256.Sum256([]byte(title + "|" + content))
	return fmt.Sprintf("%x", h)
}

// SaveNode salveaza sau actualizeaza informatiile despre un site onion dupa crawling.
// Returneaza (contentChanged bool, error). Daca hash-ul continutului nu s-a schimbat,
// face un update minimal (fara sa atinga continutul sau tsvector).
func (db *DB) SaveNode(nodeURL, title, server string, statusCode int, status string, metadata string, content string, category string) (bool, error) {
	if metadata == "" {
		metadata = "{}"
	}
	newHash := ContentHash(title, content)

	// Verificam daca hash-ul s-a schimbat fata de ultima vizita
	var oldHash sql.NullString
	_ = db.Conn.QueryRow(`SELECT content_hash FROM nodes WHERE url = $1`, nodeURL).Scan(&oldHash)

	contentChanged := !oldHash.Valid || oldHash.String != newHash

	if contentChanged {
		// Continut nou sau prima vizita: update complet
		_, err := db.Conn.Exec(`
		INSERT INTO nodes (url, title, status_code, server_header, processing_status, metadata, content, content_hash, category, last_crawled_at,
		                   next_crawl_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, CURRENT_TIMESTAMP,
		        CURRENT_TIMESTAMP + (INTERVAL '1 day' * 7))
		ON CONFLICT (url) DO UPDATE SET
			title             = EXCLUDED.title,
			status_code       = EXCLUDED.status_code,
			server_header     = EXCLUDED.server_header,
			processing_status = EXCLUDED.processing_status,
			metadata          = EXCLUDED.metadata,
			content           = EXCLUDED.content,
			content_hash      = EXCLUDED.content_hash,
			category          = EXCLUDED.category,
			last_crawled_at   = CURRENT_TIMESTAMP,
			next_crawl_at     = CURRENT_TIMESTAMP + (INTERVAL '1 day' * nodes.re_crawl_interval_days)
		WHERE nodes.processing_status != 'blocked';
		`, nodeURL, title, statusCode, server, status, metadata, content, newHash, category)
		return true, err
	}

	// Continut nemodificat: update minimal — nu atinge content/tsvector/category
	_, err := db.Conn.Exec(`
	UPDATE nodes SET
		status_code       = $2,
		server_header     = $3,
		processing_status = $4,
		last_crawled_at   = CURRENT_TIMESTAMP,
		next_crawl_at     = CURRENT_TIMESTAMP + (INTERVAL '1 day' * re_crawl_interval_days)
	WHERE url = $1 AND processing_status != 'blocked'
	`, nodeURL, statusCode, server, status)
	return false, err
}

// EnqueueURL adauga un URL in coada de crawling fara sa suprascrie date existente.
// Returneaza ErrBlacklisted daca domeniul e pe blacklist.
func (db *DB) EnqueueURL(rawURL string, depth int) error {
	if len(rawURL) > 2048 {
		return fmt.Errorf("url prea lung (max 2048 caractere)")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("url invalid: %s", rawURL)
	}
	host := strings.ToLower(parsed.Host)
	var count int
	db.Conn.QueryRow(`SELECT COUNT(*) FROM blacklist WHERE domain = $1`, host).Scan(&count)
	if count > 0 {
		return ErrBlacklisted
	}
	_, err = db.Conn.Exec(
		`INSERT INTO nodes (url, host, processing_status, depth) VALUES ($1, $2, 'pending', $3) ON CONFLICT (url) DO NOTHING`,
		rawURL, host, depth,
	)
	return err
}

// SearchNodes efectueaza o cautare Full-Text pe titlu si continut folosind indexul GIN.
// Daca category nu e gol, filtreaza si dupa categorie.
func (db *DB) SearchNodes(searchQuery, category string) ([]Node, error) {
	rows, err := db.Conn.Query(`
		SELECT id, url, COALESCE(title, ''), COALESCE(status_code, 0), COALESCE(server_header, ''),
		       processing_status, COALESCE(category, 'unknown'),
		       to_char(last_crawled_at, 'YYYY-MM-DD HH24:MI:SS')
		FROM nodes
		WHERE search_vector @@ plainto_tsquery('english', $1)
		  AND ($2 = '' OR category = $2)
		ORDER BY ts_rank(search_vector, plainto_tsquery('english', $1)) DESC
		LIMIT 50
	`, searchQuery, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		var lastCrawled sql.NullString
		if err := rows.Scan(&n.ID, &n.URL, &n.Title, &n.StatusCode, &n.ServerHeader, &n.ProcessingStatus, &n.Category, &lastCrawled); err != nil {
			return nil, err
		}
		if lastCrawled.Valid {
			n.LastCrawledAt = lastCrawled.String
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// SaveEdge creeaza o legatura intre doua site-uri si adauga target-ul in coada daca e nou.
// Daca URL-ul exista deja dar la adancime mai mare, actualizeaza adancimea (crawl mai eficient).
// Domeniile de pe blacklist sunt sarite (nu se adauga in coada).
func (db *DB) SaveEdge(source, target string, targetDepth int) error {
	targetHost := ""
	if parsed, err := url.Parse(target); err == nil {
		targetHost = strings.ToLower(parsed.Host)
	}

	// Verificam blacklist-ul inainte sa adaugam nodul tinta in coada
	if targetHost != "" {
		var count int
		db.Conn.QueryRow(`SELECT COUNT(*) FROM blacklist WHERE domain = $1`, targetHost).Scan(&count)
		if count == 0 {
			_, _ = db.Conn.Exec(
				`INSERT INTO nodes (url, host, processing_status, depth) VALUES ($1, $2, 'pending', $3)
				 ON CONFLICT (url) DO UPDATE
				   SET depth = EXCLUDED.depth
				   WHERE nodes.depth > EXCLUDED.depth AND nodes.processing_status = 'pending'`,
				target, targetHost, targetDepth,
			)
		}
	}

	_, err := db.Conn.Exec(`
	INSERT INTO edges (source_url, target_url)
	VALUES ($1, $2)
	ON CONFLICT (source_url, target_url) DO NOTHING
	`, source, target)
	return err
}

// GetNextPendingNode extrage atomic urmatorul URL care trebuie scanat.
// Prioritate: noduri 'pending' > noduri 'completed' cu next_crawl_at expirat.
func (db *DB) GetNextPendingNode() (string, int, error) {
	var nodeURL string
	var depth int
	err := db.Conn.QueryRow(`
		UPDATE nodes
		SET processing_status = 'crawling'
		WHERE url = (
			SELECT url FROM nodes
			WHERE (
				(processing_status = 'pending' AND next_crawl_at <= CURRENT_TIMESTAMP)
				OR
				(processing_status = 'completed' AND next_crawl_at <= CURRENT_TIMESTAMP)
			)
			ORDER BY
				CASE WHEN processing_status = 'pending' THEN 0 ELSE 1 END ASC,
				next_crawl_at ASC
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

// FailNodeWithRetry inregistreaza un esec si programeaza o reincercare cu exponential backoff.
// Formula: min(2^retry * 10min * rand(0.8..1.2), 48h)
func (db *DB) FailNodeWithRetry(nodeURL string) error {
	_, err := db.Conn.Exec(`
		UPDATE nodes
		SET retry_count       = retry_count + 1,
		    processing_status = CASE WHEN retry_count >= 4 THEN 'failed' ELSE 'pending' END,
		    next_crawl_at     = CURRENT_TIMESTAMP + (
		        LEAST(
		            INTERVAL '1 minute' * (10 * POW(2, retry_count)),
		            INTERVAL '48 hours'
		        )
		    )
		WHERE url = $1
	`, nodeURL)
	return err
}

// Stats retine statistici sumare despre starea crawlingului
type Stats struct {
	NodesCrawled  int
	PendingNodes  int
	FailedNodes   int
	CrawlingNodes int
	BlockedNodes  int
	TotalEdges    int
}

// GetStats returneaza statistici complete despre starea crawlingului
func (db *DB) GetStats() (Stats, error) {
	var s Stats
	err := db.Conn.QueryRow(`
		SELECT
			COUNT(*) FILTER (WHERE processing_status = 'completed'),
			COUNT(*) FILTER (WHERE processing_status = 'pending'),
			COUNT(*) FILTER (WHERE processing_status = 'failed'),
			COUNT(*) FILTER (WHERE processing_status = 'crawling'),
			COUNT(*) FILTER (WHERE processing_status = 'blocked')
		FROM nodes
	`).Scan(&s.NodesCrawled, &s.PendingNodes, &s.FailedNodes, &s.CrawlingNodes, &s.BlockedNodes)
	if err != nil {
		return s, err
	}
	err = db.Conn.QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&s.TotalEdges)
	return s, err
}

// GetNodeByURL returneaza detaliile complete ale unui nod dupa URL
func (db *DB) GetNodeByURL(nodeURL string) (*NodeDetail, error) {
	var n NodeDetail
	var lastCrawled, discovered sql.NullString
	var contentHash sql.NullString
	err := db.Conn.QueryRow(`
		SELECT id, url, COALESCE(title,''), COALESCE(status_code,0), COALESCE(server_header,''),
		       processing_status, COALESCE(category,'unknown'),
		       COALESCE(content,''), COALESCE(metadata,'{}'), content_hash,
		       to_char(last_crawled_at,'YYYY-MM-DD HH24:MI:SS'),
		       to_char(discovered_at,'YYYY-MM-DD HH24:MI:SS')
		FROM nodes WHERE url = $1
	`, nodeURL).Scan(
		&n.ID, &n.URL, &n.Title, &n.StatusCode, &n.ServerHeader,
		&n.ProcessingStatus, &n.Category,
		&n.Content, &n.Metadata, &contentHash,
		&lastCrawled, &discovered,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastCrawled.Valid {
		n.LastCrawledAt = lastCrawled.String
	}
	if discovered.Valid {
		n.DiscoveredAt = discovered.String
	}
	if contentHash.Valid {
		n.ContentHash = contentHash.String
	}
	return &n, nil
}

// RequeueForCrawl reseteaza un nod la 'pending' pentru re-crawl imediat.
// Returneaza (found bool, canRequeue bool, error).
// canRequeue=false daca nodul e deja in starea 'crawling'.
func (db *DB) RequeueForCrawl(nodeURL string) (found bool, canRequeue bool, err error) {
	var status string
	err = db.Conn.QueryRow(`SELECT processing_status FROM nodes WHERE url = $1`, nodeURL).Scan(&status)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if status == "crawling" || status == "blocked" {
		return true, false, nil
	}
	_, err = db.Conn.Exec(
		`UPDATE nodes SET processing_status = 'pending', next_crawl_at = CURRENT_TIMESTAMP WHERE url = $1`,
		nodeURL,
	)
	return true, true, err
}

// MarkRobotsBlocked marcheaza un nod ca interzis de robots.txt.
// Seteaza next_crawl_at la 30 de zile si retry_count=10 pentru a preveni re-incercari inutile.
// Nu suprascrie nodurile blocate explicit prin blacklist.
func (db *DB) MarkRobotsBlocked(nodeURL string) error {
	_, err := db.Conn.Exec(`
		UPDATE nodes SET
			processing_status = 'failed',
			retry_count       = 10,
			next_crawl_at     = CURRENT_TIMESTAMP + INTERVAL '30 days'
		WHERE url = $1 AND processing_status != 'blocked'
	`, nodeURL)
	return err
}

func (db *DB) GetNodes(limit, offset int) ([]Node, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Conn.Query(`
		SELECT id, url, COALESCE(title, ''), COALESCE(status_code, 0), COALESCE(server_header, ''),
		       processing_status, COALESCE(category, 'unknown'),
		       to_char(last_crawled_at, 'YYYY-MM-DD HH24:MI:SS')
		FROM nodes ORDER BY discovered_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		var lastCrawled sql.NullString
		if err := rows.Scan(&n.ID, &n.URL, &n.Title, &n.StatusCode, &n.ServerHeader, &n.ProcessingStatus, &n.Category, &lastCrawled); err != nil {
			return nil, err
		}
		if lastCrawled.Valid {
			n.LastCrawledAt = lastCrawled.String
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func (db *DB) GetEdges(limit, offset int) ([]Edge, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := db.Conn.Query("SELECT source_url, target_url FROM edges LIMIT $1 OFFSET $2", limit, offset)
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

// QueueSummary contine statistici despre coada de crawling
type QueueSummary struct {
	StatusCounts map[string]int `json:"status_counts"`
	NextItems    []QueueItem    `json:"next_items"`
}

// QueueItem reprezinta un URL din coada, cu metadata de baza
type QueueItem struct {
	URL          string `json:"url"`
	Depth        int    `json:"depth"`
	Status       string `json:"status"`
	DiscoveredAt string `json:"discovered_at"`
}

// GetQueueSummary returneaza numaratori pe status si urmatoarele 10 URL-uri din coada
func (db *DB) GetQueueSummary() (*QueueSummary, error) {
	rows, err := db.Conn.Query(`
		SELECT processing_status, COUNT(*) FROM nodes GROUP BY processing_status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	next, err := db.Conn.Query(`
		SELECT url, depth, processing_status, to_char(discovered_at, 'YYYY-MM-DD HH24:MI:SS')
		FROM nodes
		WHERE processing_status = 'pending'
		ORDER BY next_crawl_at ASC
		LIMIT 10
	`)
	if err != nil {
		return nil, err
	}
	defer next.Close()

	var items []QueueItem
	for next.Next() {
		var item QueueItem
		var disc sql.NullString
		if err := next.Scan(&item.URL, &item.Depth, &item.Status, &disc); err != nil {
			return nil, err
		}
		if disc.Valid {
			item.DiscoveredAt = disc.String
		}
		items = append(items, item)
	}
	return &QueueSummary{StatusCounts: counts, NextItems: items}, next.Err()
}

// AddBlacklist adauga un domeniu in blacklist (seteaza processing_status='blocked' pe toate nodurile cu acel domeniu)
// si previne adaugarea viitoare de URL-uri din acel domeniu.
func (db *DB) AddBlacklist(domain string) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	_, err := db.Conn.Exec(`
		INSERT INTO blacklist (domain) VALUES ($1) ON CONFLICT (domain) DO NOTHING
	`, domain)
	if err != nil {
		return err
	}
	// Blocheaza toate nodurile existente cu acest domeniu (match exact pe coloana host)
	_, err = db.Conn.Exec(`
		UPDATE nodes SET processing_status = 'blocked'
		WHERE host = $1 AND processing_status != 'blocked'
	`, domain)
	return err
}

// GetBlacklist returneaza toate domeniile blocate
func (db *DB) GetBlacklist() ([]string, error) {
	rows, err := db.Conn.Query(`SELECT domain FROM blacklist ORDER BY added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var domains []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		domains = append(domains, d)
	}
	return domains, rows.Err()
}

// IsDomainBlacklisted verifica daca un domeniu e blocat
func (db *DB) IsDomainBlacklisted(domain string) (bool, error) {
	var count int
	err := db.Conn.QueryRow(`SELECT COUNT(*) FROM blacklist WHERE domain = $1`, strings.ToLower(domain)).Scan(&count)
	return count > 0, err
}

// ExportNodes returneaza toate nodurile crawlate complet, in ordine de descoperire.
// Foloseste un cursor pentru a nu incarca toata tabela in memorie.
func (db *DB) ExportNodes(fn func(Node) error) error {
	rows, err := db.Conn.Query(`
		SELECT id, url, COALESCE(title,''), COALESCE(status_code,0), COALESCE(server_header,''),
		       processing_status, COALESCE(category,'unknown'),
		       to_char(last_crawled_at,'YYYY-MM-DD HH24:MI:SS')
		FROM nodes
		WHERE processing_status = 'completed'
		ORDER BY discovered_at ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n Node
		var lastCrawled sql.NullString
		if err := rows.Scan(&n.ID, &n.URL, &n.Title, &n.StatusCode, &n.ServerHeader,
			&n.ProcessingStatus, &n.Category, &lastCrawled); err != nil {
			return err
		}
		if lastCrawled.Valid {
			n.LastCrawledAt = lastCrawled.String
		}
		if err := fn(n); err != nil {
			return err
		}
	}
	return rows.Err()
}

// DeleteBlacklist elimina un domeniu din blacklist si repune nodurile blocate inapoi in coada.
// Returneaza (found bool, error).
func (db *DB) DeleteBlacklist(domain string) (bool, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	res, err := db.Conn.Exec(`DELETE FROM blacklist WHERE domain = $1`, domain)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return false, nil
	}
	_, err = db.Conn.Exec(`
		UPDATE nodes SET processing_status = 'pending', retry_count = 0, next_crawl_at = CURRENT_TIMESTAMP
		WHERE host = $1 AND processing_status = 'blocked'
	`, domain)
	return true, err
}

// TimelineStat retine numarul de noduri descoperite intr-o zi.
type TimelineStat struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// GetTimelineStats returneaza numarul de noduri descoperite pe zi in ultimele 30 de zile.
func (db *DB) GetTimelineStats() ([]TimelineStat, error) {
	rows, err := db.Conn.Query(`
		SELECT to_char(discovered_at, 'YYYY-MM-DD') AS date, COUNT(*) AS count
		FROM nodes
		WHERE discovered_at >= CURRENT_DATE - INTERVAL '30 days'
		GROUP BY date
		ORDER BY date ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stats []TimelineStat
	for rows.Next() {
		var s TimelineStat
		if err := rows.Scan(&s.Date, &s.Count); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}
