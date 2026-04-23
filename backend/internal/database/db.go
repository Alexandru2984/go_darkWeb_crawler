package database

import (
	"context"
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
		`ALTER TABLE nodes DROP CONSTRAINT IF EXISTS nodes_url_key CASCADE`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS user_id INT NOT NULL DEFAULT 1 REFERENCES users(id) ON DELETE CASCADE`,
		`ALTER TABLE nodes ADD CONSTRAINT nodes_url_user_key UNIQUE (url, user_id)`,
		`ALTER TABLE edges ADD COLUMN IF NOT EXISTS user_id INT NOT NULL DEFAULT 1 REFERENCES users(id) ON DELETE CASCADE`,
		`ALTER TABLE edges DROP CONSTRAINT IF EXISTS edges_pkey CASCADE`,
		`ALTER TABLE edges ADD CONSTRAINT edges_pkey PRIMARY KEY (source_url, target_url, user_id)`,
		`ALTER TABLE edges ADD CONSTRAINT edges_source_url_fkey FOREIGN KEY (source_url, user_id) REFERENCES nodes(url, user_id) ON DELETE CASCADE`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_search_vector ON nodes USING GIN(search_vector)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(processing_status, next_crawl_at)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_category ON nodes(category)`,
		// Email verification token expiry (security hardening)
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS verification_expires_at TIMESTAMP`,
		`UPDATE users SET verification_expires_at = CURRENT_TIMESTAMP + INTERVAL '24 hours'
		   WHERE is_verified = FALSE AND verification_token IS NOT NULL AND verification_expires_at IS NULL`,
		// Audit log pentru evenimente de securitate (login, register, token invalid etc)
		`CREATE TABLE IF NOT EXISTS auth_audit (
			id         SERIAL PRIMARY KEY,
			event      TEXT NOT NULL,
			email      TEXT,
			ip         TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_audit_created ON auth_audit(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_audit_email_event ON auth_audit(email, event, created_at DESC)`,
		// Heartbeat pentru detectarea nodurilor blocate in 'crawling' dupa crash brutal
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS crawl_started_at TIMESTAMP`,
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
	UserID           int    `json:"user_id"`
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
func (db *DB) SaveNode(nodeURL, title, server string, statusCode int, status string, metadata string, content string, category string, userID int) (bool, error) {
	if metadata == "" {
		metadata = "{}"
	}
	newHash := ContentHash(title, content)

	// Verificam daca hash-ul s-a schimbat fata de ultima vizita
	var oldHash sql.NullString
	_ = db.Conn.QueryRow(`SELECT content_hash FROM nodes WHERE url = $1 AND user_id = $2`, nodeURL, userID).Scan(&oldHash)

	contentChanged := !oldHash.Valid || oldHash.String != newHash

	if contentChanged {
		// Continut nou sau prima vizita: update complet
		_, err := db.Conn.Exec(`
		INSERT INTO nodes (url, title, status_code, server_header, processing_status, metadata, content, content_hash, category, last_crawled_at, next_crawl_at, user_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP + (INTERVAL '1 day' * 7), $10) ON CONFLICT (url, user_id) DO UPDATE SET
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
		`, nodeURL, userID, title, statusCode, server, status, metadata, content, newHash, category, userID)
		return true, err
	}

	// Continut nemodificat: update minimal — nu atinge content/tsvector/category
	_, err := db.Conn.Exec(`
	UPDATE nodes SET
		status_code       = $3,
		server_header     = $4,
		processing_status = $5,
		last_crawled_at   = CURRENT_TIMESTAMP,
		next_crawl_at     = CURRENT_TIMESTAMP + (INTERVAL '1 day' * re_crawl_interval_days)
	WHERE url = $1 AND user_id = $2 AND processing_status != 'blocked'
	`, nodeURL, userID, statusCode, server, status)
	return false, err
}

// EnqueueURL adauga un URL in coada de crawling fara sa suprascrie date existente.
// Returneaza ErrBlacklisted daca domeniul e pe blacklist.
func (db *DB) EnqueueURL(rawURL string, depth int, userID int) error {
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
		`INSERT INTO nodes (url, host, processing_status, depth, user_id) VALUES ($1, $2, 'pending', $3, $4) ON CONFLICT (url, user_id) DO NOTHING`,
		rawURL, host, depth, userID,
	)
	return err
}

// SearchNodes efectueaza o cautare Full-Text pe titlu si continut folosind indexul GIN.
// Daca category nu e gol, filtreaza si dupa categorie.
func (db *DB) SearchNodes(searchQuery, category string, userID int, isAdmin bool) ([]Node, error) {
	rows, err := db.Conn.Query(`
		SELECT id, url, COALESCE(title, ''), COALESCE(status_code, 0), COALESCE(server_header, ''),
		       processing_status, COALESCE(category, 'unknown'),
		       to_char(last_crawled_at, 'YYYY-MM-DD HH24:MI:SS')
		FROM nodes
		WHERE search_vector @@ plainto_tsquery('english', $1)
		  AND ($2 = '' OR category = $2)
		  AND (user_id = $3 OR $4)
		ORDER BY ts_rank(search_vector, plainto_tsquery('english', $1)) DESC
		LIMIT 50
	`, searchQuery, category, userID, isAdmin)
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
func (db *DB) SaveEdge(source, target string, targetDepth int, userID int) error {
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
				`INSERT INTO nodes (url, host, processing_status, depth, user_id) VALUES ($1, $2, 'pending', $3, $4) ON CONFLICT (url, user_id) DO UPDATE
				   SET depth = EXCLUDED.depth
				   WHERE nodes.depth > EXCLUDED.depth AND nodes.processing_status = 'pending'`,
				target, targetHost, targetDepth, userID,
			)
		}
	}

	_, err := db.Conn.Exec(`
	INSERT INTO edges (source_url, target_url, user_id) VALUES ($1, $2, $3) ON CONFLICT (source_url, target_url, user_id) DO NOTHING
	`, source, target, userID)
	return err
}

// GetNextPendingNode extrage atomic urmatorul URL care trebuie scanat.
// Prioritate: noduri 'pending' > noduri 'completed' cu next_crawl_at expirat.
// Seteaza crawl_started_at pentru a permite sweeperul sa recupereze noduri stuck.
func (db *DB) GetNextPendingNode() (string, int, int, error) {
	var nodeURL string
	var depth int
	var userID int
	err := db.Conn.QueryRow(`
		UPDATE nodes
		SET processing_status = 'crawling',
		    crawl_started_at  = CURRENT_TIMESTAMP
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
		RETURNING url, depth, user_id
	`).Scan(&nodeURL, &depth, &userID)
	if err == sql.ErrNoRows {
		return "", 0, 0, nil
	}
	return nodeURL, depth, userID, err
}

// ResetStuckCrawling reseteaza nodurile ramase in starea 'crawling' mai mult
// decat olderThan (ex: dupa crash brutal al unui worker). Returneaza numarul
// de noduri recuperate.
func (db *DB) ResetStuckCrawling(olderThan time.Duration) (int64, error) {
	res, err := db.Conn.Exec(`
		UPDATE nodes
		SET processing_status = 'pending',
		    crawl_started_at  = NULL
		WHERE processing_status = 'crawling'
		  AND crawl_started_at IS NOT NULL
		  AND crawl_started_at < CURRENT_TIMESTAMP - ($1 || ' seconds')::INTERVAL
	`, fmt.Sprintf("%d", int(olderThan.Seconds())))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// FailNodeWithRetry inregistreaza un esec si programeaza o reincercare cu exponential backoff.
// Formula: min(2^retry * 10min, 48h). Nodurile 'blocked' nu sunt niciodata modificate.
func (db *DB) FailNodeWithRetry(nodeURL string, userID int) error {
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
		WHERE url = $1 AND user_id = $2 AND processing_status != 'blocked'
	`, nodeURL, userID)
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
func (db *DB) GetStats(userID int, isAdmin bool) (Stats, error) {
	var s Stats
	err := db.Conn.QueryRow(`SELECT COUNT(*) FILTER (WHERE processing_status = 'completed'), COUNT(*) FILTER (WHERE processing_status = 'pending' AND user_id = $1), COUNT(*) FILTER (WHERE processing_status = 'failed'), COUNT(*) FILTER (WHERE processing_status = 'crawling'), COUNT(*) FILTER (WHERE processing_status = 'blocked') FROM nodes WHERE (user_id = $1 OR $2) `, userID, isAdmin).Scan(&s.NodesCrawled, &s.PendingNodes, &s.FailedNodes, &s.CrawlingNodes, &s.BlockedNodes)
	if err != nil {
		return s, err
	}
	err = db.Conn.QueryRow(`SELECT COUNT(*) FROM edges WHERE (user_id = $1 OR $2)`, userID, isAdmin).Scan(&s.TotalEdges)
	return s, err
}

// GetNodeByURL returneaza detaliile complete ale unui nod dupa URL
func (db *DB) GetNodeByURL(nodeURL string, userID int, isAdmin bool) (*NodeDetail, error) {
	var n NodeDetail
	var lastCrawled, discovered sql.NullString
	var contentHash sql.NullString
	err := db.Conn.QueryRow(`
		SELECT id, url, COALESCE(title,''), COALESCE(status_code,0), COALESCE(server_header,''),
		       processing_status, COALESCE(category,'unknown'),
		       COALESCE(content,''), COALESCE(metadata,'{}'), content_hash,
		       to_char(last_crawled_at,'YYYY-MM-DD HH24:MI:SS'),
		       to_char(discovered_at,'YYYY-MM-DD HH24:MI:SS')
		FROM nodes WHERE url = $1 AND (user_id = $2 OR $3)
	`, nodeURL, userID, isAdmin).Scan(
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
func (db *DB) RequeueForCrawl(nodeURL string, userID int) (found bool, canRequeue bool, err error) {
	var status string
	err = db.Conn.QueryRow(`SELECT processing_status FROM nodes WHERE url = $1 AND user_id = $2`, nodeURL, userID).Scan(&status)
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
		`UPDATE nodes SET processing_status = 'pending', next_crawl_at = CURRENT_TIMESTAMP WHERE url = $1 AND user_id = $2`,
		nodeURL, userID,
	)
	return true, true, err
}

// MarkRobotsBlocked marcheaza un nod ca interzis de robots.txt.
// Seteaza next_crawl_at la 30 de zile si retry_count=10 pentru a preveni re-incercari inutile.
// Nu suprascrie nodurile blocate explicit prin blacklist.
func (db *DB) MarkRobotsBlocked(nodeURL string, userID int) error {
	_, err := db.Conn.Exec(`
		UPDATE nodes SET
			processing_status = 'failed',
			retry_count       = 10,
			next_crawl_at     = CURRENT_TIMESTAMP + INTERVAL '30 days'
		WHERE url = $1 AND user_id = $2 AND processing_status != 'blocked'
	`, nodeURL, userID)
	return err
}

func (db *DB) GetNodes(limit, offset int, userID int, isAdmin bool) ([]Node, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Conn.Query(`
		SELECT id, url, COALESCE(title, ''), COALESCE(status_code, 0), COALESCE(server_header, ''),
		       processing_status, COALESCE(category, 'unknown'),
		       to_char(last_crawled_at, 'YYYY-MM-DD HH24:MI:SS')
		FROM nodes WHERE (user_id = $3 OR $4) ORDER BY discovered_at DESC LIMIT $1 OFFSET $2`, limit, offset, userID, isAdmin)
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

func (db *DB) GetEdges(limit, offset int, userID int, isAdmin bool) ([]Edge, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := db.Conn.Query(`SELECT source_url, target_url FROM edges WHERE (user_id = $3 OR $4) LIMIT $1 OFFSET $2`, limit, offset, userID, isAdmin)
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
func (db *DB) GetQueueSummary(userID int, isAdmin bool) (*QueueSummary, error) {
	rows, err := db.Conn.Query(`
		SELECT processing_status, COUNT(*) FROM nodes
		WHERE (user_id = $1 OR $2)
		GROUP BY processing_status
	`, userID, isAdmin)
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
		WHERE processing_status = 'pending' AND (user_id = $1 OR $2)
		ORDER BY next_crawl_at ASC
		LIMIT 10
	`, userID, isAdmin)
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
// ctx permite anularea exportului daca clientul se deconecteaza.
// Foloseste un cursor pentru a nu incarca toata tabela in memorie.
func (db *DB) ExportNodes(ctx context.Context, userID int, isAdmin bool, fn func(Node) error) error {
	rows, err := db.Conn.QueryContext(ctx, `
		SELECT id, url, COALESCE(title,''), COALESCE(status_code,0), COALESCE(server_header,''),
		       processing_status, COALESCE(category,'unknown'),
		       to_char(last_crawled_at,'YYYY-MM-DD HH24:MI:SS')
		FROM nodes
		WHERE processing_status = 'completed' AND (user_id = $1 OR $2)
		ORDER BY discovered_at ASC
	`, userID, isAdmin)
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

// GraphMLEdge reprezinta o muchie exportata pentru GraphML — ambele capete sunt noduri completed.
type GraphMLEdge struct {
	SourceID int
	TargetID int
}

// ExportGraphMLEdges returneaza muchiile unde ambele noduri (source si target) sunt completed.
// Foloseste JOIN pentru a garanta consistenta grafului exportat.
func (db *DB) ExportGraphMLEdges(ctx context.Context, userID int, isAdmin bool, fn func(GraphMLEdge) error) error {
	rows, err := db.Conn.QueryContext(ctx, `
		SELECT n1.id, n2.id
		FROM edges e
		JOIN nodes n1 ON n1.url = e.source_url AND n1.processing_status = 'completed'
		JOIN nodes n2 ON n2.url = e.target_url AND n2.processing_status = 'completed'
		WHERE (e.user_id = $1 OR $2)
	`, userID, isAdmin)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ge GraphMLEdge
		if err := rows.Scan(&ge.SourceID, &ge.TargetID); err != nil {
			return err
		}
		if err := fn(ge); err != nil {
			return err
		}
	}
	return rows.Err()
}

type TimelineStat struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// GetTimelineStats returneaza numarul de noduri descoperite pe zi in ultimele 30 de zile.
func (db *DB) GetTimelineStats(userID int, isAdmin bool) ([]TimelineStat, error) {
	rows, err := db.Conn.Query(`
		SELECT to_char(discovered_at, 'YYYY-MM-DD') AS date, COUNT(*) AS count
		FROM nodes
		WHERE discovered_at >= CURRENT_DATE - INTERVAL '30 days' AND (user_id = $1 OR $2)
		GROUP BY date
		ORDER BY date ASC
	`, userID, isAdmin)
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

// Model pentru Useri
type User struct {
	ID                int    `json:"id"`
	Email             string `json:"email"`
	PasswordHash      string `json:"-"`
	Role              string `json:"role"`
	IsVerified        bool   `json:"is_verified"`
	VerificationToken string `json:"-"`
	CreatedAt         string `json:"created_at"`
}

// NormalizeEmail returneaza emailul in lowercase si fara spatii.
// Toate operatiile cu email-uri trebuie sa treaca prin asta.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// CreateUser adauga un nou utilizator cu token de verificare valabil 24h.
func (db *DB) CreateUser(email, passwordHash, role, token string) error {
	_, err := db.Conn.Exec(`
		INSERT INTO users (email, password_hash, role, verification_token, verification_expires_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP + INTERVAL '24 hours')
	`, NormalizeEmail(email), passwordHash, role, token)
	return err
}

// GetUserByEmail extrage un utilizator folosind email-ul (case-insensitive).
func (db *DB) GetUserByEmail(email string) (*User, error) {
	var u User
	err := db.Conn.QueryRow(`
		SELECT id, email, password_hash, role, is_verified, COALESCE(verification_token, ''), to_char(created_at, 'YYYY-MM-DD HH24:MI:SS')
		FROM users WHERE email = $1
	`, NormalizeEmail(email)).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.IsVerified, &u.VerificationToken, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

// VerifyUser seteaza utilizatorul ca verificat, doar daca token-ul nu a expirat.
func (db *DB) VerifyUser(token string) error {
	if len(token) < 16 {
		return errors.New("token invalid")
	}
	res, err := db.Conn.Exec(`
		UPDATE users
		SET is_verified = TRUE, verification_token = NULL, verification_expires_at = NULL
		WHERE verification_token = $1
		  AND is_verified = FALSE
		  AND (verification_expires_at IS NULL OR verification_expires_at > CURRENT_TIMESTAMP)
	`, token)
	if err != nil {
		return err
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return errors.New("token invalid, expirat sau deja folosit")
	}
	return nil
}

// HasAnyAdmin verifica daca exista cel putin un user cu rol admin.
// Folosit la bootstrap — primul user cu ADMIN_EMAIL devine admin doar daca nu exista inca.
func (db *DB) HasAnyAdmin() (bool, error) {
	var count int
	err := db.Conn.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&count)
	return count > 0, err
}

// LogAuthEvent insereaza un record in auth_audit. Nu bloca fluxul daca esueaza.
func (db *DB) LogAuthEvent(event, email, ip string) {
	_, err := db.Conn.Exec(
		`INSERT INTO auth_audit (event, email, ip) VALUES ($1, $2, $3)`,
		event, NormalizeEmail(email), ip,
	)
	if err != nil {
		log.Printf("[audit] nu am putut loga %s pt %s: %v", event, email, err)
	}
}

// CountRecentAuthEvents numara evenimentele din auth_audit de tip `event` pentru `email`
// in ultimele `window` minute. Folosit pentru:
//   - lockout login dupa 5 fail consecutiv ('login_fail')
//   - rate-limit register per email destinatar ('register_ok')
func (db *DB) CountRecentAuthEvents(event, email string, windowMinutes int) (int, error) {
	var count int
	err := db.Conn.QueryRow(`
		SELECT COUNT(*) FROM auth_audit
		WHERE event = $1 AND email = $2
		  AND created_at > CURRENT_TIMESTAMP - ($3 || ' minutes')::INTERVAL
	`, event, NormalizeEmail(email), fmt.Sprintf("%d", windowMinutes)).Scan(&count)
	return count, err
}

// GetUserRole returneaza rolul curent al utilizatorului din DB (bypass JWT claims).
// Folosit pe endpointurile admin-only pentru a invalida imediat demotion-urile,
// in loc sa astepti expirarea JWT-ului.
func (db *DB) GetUserRole(userID int) (string, error) {
	var role string
	err := db.Conn.QueryRow(`SELECT role FROM users WHERE id = $1`, userID).Scan(&role)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return role, err
}

