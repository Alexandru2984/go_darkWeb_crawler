package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"onion-spider/internal/auth"
	"onion-spider/internal/crawler"
	"onion-spider/internal/database"
	"onion-spider/internal/email"
	"onion-spider/internal/proxy"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	gofpdf "github.com/go-pdf/fpdf"
	"github.com/joho/godotenv"
	"github.com/xuri/excelize/v2"
)

// emailRegex e o verificare basic pentru format — nu validarea RFC 5322 completa,
// doar sa filtram input-uri evident invalide inainte sa le trimitem la SMTP/DB.
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// crawlLimiter este un rate limiter per-IP cu fereastra fixa.
type crawlLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*limitBucket
	limit      int
	window     time.Duration
	maxBuckets int
}

type limitBucket struct {
	count   int
	resetAt time.Time
}

func newCrawlLimiter(limit int, window time.Duration) *crawlLimiter {
	l := &crawlLimiter{
		buckets:    make(map[string]*limitBucket),
		limit:      limit,
		window:     window,
		maxBuckets: 100_000,
	}
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
		if !ok && len(l.buckets) >= l.maxBuckets {
			for k, v := range l.buckets {
				if now.After(v.resetAt) {
					delete(l.buckets, k)
				}
			}
			if len(l.buckets) >= l.maxBuckets {
				return false
			}
		}
		l.buckets[ip] = &limitBucket{count: 1, resetAt: now.Add(l.window)}
		return true
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	return true
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
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset = (page - 1) * limit
	return
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// clientIP extrage IP-ul clientului. Backend-ul e legat pe 127.0.0.1 si primeste
// doar cereri prin nginx; nginx seteaza X-Real-IP = $remote_addr, pe care chi's
// middleware.RealIP il pune in r.RemoteAddr. Deci valoarea aici vine de la nginx,
// nu de la clientul HTTP direct — nu e spoofabila.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

type contextKey string

const (
	userContextKey   contextKey = "user"
	dbRoleContextKey contextKey = "db_role"
)

// jwtMiddleware extrage claims-urile din Authorization header daca exista si sunt valide.
// Fara header: pass-through (endpointurile publice functioneaza).
// Header prezent dar invalid: 401 (nu lasam tokene forge sa treaca ca unauth).
func jwtMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := auth.ValidateToken(tokenStr)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "Token invalid sau expirat")
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Value(userContextKey).(*auth.Claims); !ok {
			writeJSONError(w, http.StatusUnauthorized, "Necesita autentificare")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loadDBRole citeste rolul curent din DB si il pune in context.
// Previne ca un admin demotiv prin UPDATE SQL sa pastreze privilegii pana
// expira JWT-ul (4h). Valoarea din JWT claims NU mai e folosita pentru authz.
func loadDBRole(db *database.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid := getUserID(r)
			if uid == 0 {
				next.ServeHTTP(w, r)
				return
			}
			role, err := db.GetUserRole(uid)
			if err != nil {
				log.Printf("[ERROR] loadDBRole uid=%d: %v", uid, err)
				writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
				return
			}
			ctx := context.WithValue(r.Context(), dbRoleContextKey, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requireAdminDB blocheaza requesturile daca rolul din DB (incarcat de loadDBRole)
// nu e 'admin'. TREBUIE precedat de loadDBRole in lantul de middleware.
func requireAdminDB(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if getUserID(r) == 0 {
			writeJSONError(w, http.StatusUnauthorized, "Necesita autentificare")
			return
		}
		if !isAdmin(r) {
			writeJSONError(w, http.StatusForbidden, "Necesita rol admin")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func getUserID(r *http.Request) int {
	claims, ok := r.Context().Value(userContextKey).(*auth.Claims)
	if !ok || claims == nil {
		return 0
	}
	return claims.UserID
}

// isAdmin citeste rolul din DB (via loadDBRole middleware), NU din JWT claims.
// Asigura ca un admin demotiv pierde imediat privilegiile, fara sa astepte expirarea JWT.
// Daca middleware-ul loadDBRole nu a rulat (endpoint public), returneaza false.
func isAdmin(r *http.Request) bool {
	role, ok := r.Context().Value(dbRoleContextKey).(string)
	return ok && role == "admin"
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️  Nu am gasit .env, folosesc variabilele din sistem")
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("FATAL: lipseste DATABASE_URL")
	}

	// Forteaza incarcarea JWT_SECRET la pornire — log.Fatal daca lipseste sau e slab.
	auth.MustInitSecrets()

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

	corsOrigin := os.Getenv("CORS_ORIGIN")
	if corsOrigin == "" {
		corsOrigin = "http://localhost:5173"
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(safeLogger("/api/auth/verify"))
	r.Use(middleware.Recoverer)
	r.Use(jwtMiddleware)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: strings.Split(corsOrigin, ","),
		AllowedMethods: []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Content-Type", "Authorization"},
		MaxAge:         300,
	}))

	// Rate limiters: separat per tip de operatie.
	crawlLim := newCrawlLimiter(20, time.Minute)      // /api/crawl*, /api/recrawl
	searchLim := newCrawlLimiter(60, time.Minute)     // /api/search
	loginLim := newCrawlLimiter(5, time.Minute)       // /api/auth/login
	registerLim := newCrawlLimiter(3, time.Hour)      // /api/auth/register
	verifyLim := newCrawlLimiter(10, time.Minute)     // /api/auth/verify

	dbConn, err := database.InitDB(dsn)
	if err != nil {
		log.Fatalf("FATAL: conectare DB: %v", err)
	}

	// =========================================================================
	// AUTH ENDPOINTS (publice, rate-limited, audit-logged)
	// =========================================================================

	r.Post("/api/auth/register", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !registerLim.allow(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "Prea multe inregistrari de la acest IP. Incearca din nou mai tarziu.")
			return
		}
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Date invalide")
			return
		}
		req.Email = database.NormalizeEmail(req.Email)
		if !emailRegex.MatchString(req.Email) || len(req.Email) > 254 {
			writeJSONError(w, http.StatusBadRequest, "Email invalid")
			return
		}
		if err := validatePassword(req.Password); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Rate-limit per adresa destinatar — protejeaza cota Gmail impotriva abuzului.
		// Max 3 tentative de register la acelasi email per ora.
		if n, err := dbConn.CountRecentAuthEvents("register_ok", req.Email, 60); err == nil && n >= 3 {
			log.Printf("[AUDIT] register_blocked ip=%s email=%s count=%d", sanitizeForLog(ip), sanitizeForLog(req.Email), n)
			writeJSONError(w, http.StatusTooManyRequests, "Acest email a primit deja prea multe emailuri de verificare. Incearca peste o ora.")
			return
		}

		// Rol default: user. Bootstrap-ul de admin e controlat prin ADMIN_EMAIL
		// si este permis DOAR daca nu exista inca niciun admin in sistem.
		role := "user"
		adminEmail := database.NormalizeEmail(os.Getenv("ADMIN_EMAIL"))
		if adminEmail != "" && req.Email == adminEmail {
			hasAdmin, _ := dbConn.HasAnyAdmin()
			if !hasAdmin {
				role = "admin"
			}
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Parola nu poate fi procesata")
			return
		}
		token := auth.GenerateVerificationToken()

		if err := dbConn.CreateUser(req.Email, hash, role, token); err != nil {
			log.Printf("[AUDIT] register_fail ip=%s email=%s: %v", sanitizeForLog(ip), sanitizeForLog(req.Email), err)
			dbConn.LogAuthEvent("register_fail", req.Email, ip)
			writeJSONError(w, http.StatusBadRequest, "Eroare: email deja folosit sau date invalide")
			return
		}

		dbConn.LogAuthEvent("register_ok", req.Email, ip)
		log.Printf("[AUDIT] register_ok ip=%s email=%s role=%s", sanitizeForLog(ip), sanitizeForLog(req.Email), role)

		go func() {
			if err := email.SendVerificationEmail(req.Email, token); err != nil {
				log.Printf("[email] eroare trimitere catre %s: %v", sanitizeForLog(req.Email), err)
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"message": "Cont creat! Verifica emailul."})
	})

	r.Post("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !loginLim.allow(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "Prea multe incercari de login. Incearca din nou in 1 minut.")
			return
		}
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Date invalide")
			return
		}
		req.Email = database.NormalizeEmail(req.Email)
		if req.Email == "" || req.Password == "" {
			writeJSONError(w, http.StatusBadRequest, "Email si parola obligatorii")
			return
		}

		// Account lockout: dupa 5 login_fail in 15min pe acelasi email → 15min timeout.
		// Protejeaza impotriva brute-force distribuit pe mai multe IP-uri.
		if n, err := dbConn.CountRecentAuthEvents("login_fail", req.Email, 15); err == nil && n >= 5 {
			// Ruleaza bcrypt oricum pentru a mentine timpul constant (evita detectia lockout-ului).
			auth.CheckAgainstDummy(req.Password)
			dbConn.LogAuthEvent("login_locked", req.Email, ip)
			log.Printf("[AUDIT] login_locked ip=%s email=%s count=%d", sanitizeForLog(ip), sanitizeForLog(req.Email), n)
			writeJSONError(w, http.StatusTooManyRequests, "Cont blocat temporar din cauza prea multor incercari esuate. Asteapta 15 minute.")
			return
		}

		user, err := dbConn.GetUserByEmail(req.Email)
		if err != nil {
			log.Printf("[ERROR] GetUserByEmail: %v", err)
			// Rulam bcrypt ca sa pastram timpul constant chiar si pe eroare DB.
			auth.CheckAgainstDummy(req.Password)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}
		// TIMING ATTACK MITIGATION: chiar si pe user inexistent, rulam bcrypt
		// pe un hash dummy. Fara asta, diferenta de timp (700ms vs 100ms) permite
		// enumerarea emailurilor inregistrate.
		if user == nil {
			auth.CheckAgainstDummy(req.Password)
			dbConn.LogAuthEvent("login_fail", req.Email, ip)
			log.Printf("[AUDIT] login_fail ip=%s email=%s reason=unknown_user", sanitizeForLog(ip), sanitizeForLog(req.Email))
			writeJSONError(w, http.StatusUnauthorized, "Credentiale invalide")
			return
		}
		if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
			dbConn.LogAuthEvent("login_fail", req.Email, ip)
			log.Printf("[AUDIT] login_fail ip=%s email=%s reason=bad_password", sanitizeForLog(ip), sanitizeForLog(req.Email))
			writeJSONError(w, http.StatusUnauthorized, "Credentiale invalide")
			return
		}

		if !user.IsVerified {
			dbConn.LogAuthEvent("login_unverified", req.Email, ip)
			writeJSONError(w, http.StatusForbidden, "Contul nu este verificat inca")
			return
		}

		token, err := auth.GenerateToken(user.ID, user.Email, user.Role)
		if err != nil {
			log.Printf("[ERROR] generare JWT pentru %s: %v", sanitizeForLog(user.Email), err)
			writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
			return
		}

		dbConn.LogAuthEvent("login_ok", req.Email, ip)
		log.Printf("[AUDIT] login_ok ip=%s email=%s role=%s", sanitizeForLog(ip), sanitizeForLog(user.Email), user.Role)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": token, "role": user.Role, "email": user.Email})
	})

	r.Get("/api/auth/verify", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !verifyLim.allow(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "Prea multe incercari. Incearca din nou in 1 minut.")
			return
		}
		token := r.URL.Query().Get("token")
		if len(token) < 16 || len(token) > 128 {
			writeJSONError(w, http.StatusBadRequest, "Token invalid")
			return
		}
		if err := dbConn.VerifyUser(token); err != nil {
			log.Printf("[AUDIT] verify_fail ip=%s: %v", sanitizeForLog(ip), err)
			writeJSONError(w, http.StatusBadRequest, "Token invalid, expirat sau deja folosit")
			return
		}
		log.Printf("[AUDIT] verify_ok ip=%s", sanitizeForLog(ip))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>Cont verificat</title></head><body><h1>Contul a fost verificat cu succes!</h1><p><a href="/">Inapoi la login</a></p></body></html>`))
	})

	// =========================================================================
	// STATUS (public — arata doar ca serverul e pornit; datele private cer auth)
	// =========================================================================

	engine := crawler.NewEngine(dbConn, torProxy, workers, maxDepth)

	torCtrlAddr := os.Getenv("TOR_CONTROL_ADDR")
	if torCtrlAddr == "" {
		torCtrlAddr = "127.0.0.1:9051"
	}
	torCtrl := proxy.NewTorController(
		torCtrlAddr,
		os.Getenv("TOR_CONTROL_PASSWORD"),
		os.Getenv("TOR_CONTROL_COOKIE"),
		30*time.Second,
	)
	if _, err := torCtrl.RenewCircuit(); err != nil {
		log.Printf("⚠️  TorController: control port indisponibil, reinnoire circuit dezactivata: %v", err)
	} else {
		engine.TorCtrl = torCtrl
		log.Println("✅ TorController activ")
	}

	engine.Start()

	// Sweeper: reseteaza nodurile stuck in 'crawling' (dupa crash brutal al unui worker).
	// Ruleaza la fiecare minut, muta inapoi la 'pending' nodurile cu crawl_started_at > 10min.
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			n, err := dbConn.ResetStuckCrawling(10 * time.Minute)
			if err != nil {
				log.Printf("[sweeper] ResetStuckCrawling: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("[sweeper] recuperat %d noduri stuck in 'crawling'", n)
			}
		}
	}()

	// Export limiter: max 1 export concurent PER USER (nu global), cu limita globala
	// totala la 4 ca sa nu se ajunga la OOM daca multi useri pornesc exporturi mari in acelasi timp.
	exportGlobalSem := make(chan struct{}, 4)
	exportPerUser := &sync.Map{} // map[int]chan struct{}

	r.With(loadDBRole(dbConn)).Get("/api/status", func(w http.ResponseWriter, r *http.Request) {
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
		stats, err := dbConn.GetStats(getUserID(r), isAdmin(r))
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

	// =========================================================================
	// AUTHENTICATED ENDPOINTS (orice user logat)
	// =========================================================================

	r.Group(func(r chi.Router) {
		r.Use(requireAuth)
		r.Use(loadDBRole(dbConn))

		r.Get("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			limit, offset := parsePagination(r)
			nodes, err := dbConn.GetNodes(limit, offset, getUserID(r), isAdmin(r))
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
			node, err := dbConn.GetNodeByURL(nodeURL, getUserID(r), isAdmin(r))
			if err != nil {
				log.Printf("[ERROR] GET /api/node url=%s: %v", sanitizeForLog(nodeURL), err)
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
			edges, err := dbConn.GetEdges(limit, offset, getUserID(r), isAdmin(r))
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
			if !searchLim.allow(ip) {
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
			nodes, err := dbConn.SearchNodes(q, category, getUserID(r), isAdmin(r))
			if err != nil {
				log.Printf("[ERROR] GET /api/search q=%s: %v", sanitizeForLog(q), err)
				writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
				return
			}
			json.NewEncoder(w).Encode(nodes)
		})

		r.Post("/api/crawl", func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !isAdmin(r) && !crawlLim.allow(ip) {
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
			log.Printf("[AUDIT] POST /api/crawl ip=%s user=%d url=%s", sanitizeForLog(ip), getUserID(r), sanitizeForLog(req.URL))
			if err := engine.AddToQueue(req.URL, getUserID(r)); err != nil {
				if errors.Is(err, database.ErrBlacklisted) {
					writeJSONError(w, http.StatusForbidden, "Domeniu blocat")
					return
				}
				log.Printf("[ERROR] POST /api/crawl user=%d url=%s: %v", getUserID(r), sanitizeForLog(req.URL), err)
				writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"message": "URL adaugat in coada de crawling"})
		})

		r.Post("/api/recrawl", func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !isAdmin(r) && !crawlLim.allow(ip) {
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
				writeJSONError(w, http.StatusBadRequest, "URL invalid")
				return
			}
			found, canRequeue, err := dbConn.RequeueForCrawl(req.URL, getUserID(r))
			if err != nil {
				log.Printf("[ERROR] POST /api/recrawl url=%s: %v", sanitizeForLog(req.URL), err)
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

		r.Post("/api/crawl/bulk", func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !isAdmin(r) && !crawlLim.allow(ip) {
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
				log.Printf("[AUDIT] POST /api/crawl/bulk ip=%s user=%d url=%s", sanitizeForLog(ip), getUserID(r), sanitizeForLog(u))
				if err := engine.AddToQueue(u, getUserID(r)); err != nil {
					skipped++
				} else {
					added++
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]int{"added": added, "skipped": skipped})
		})

		r.Get("/api/queue", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			summary, err := dbConn.GetQueueSummary(getUserID(r), isAdmin(r))
			if err != nil {
				log.Printf("[ERROR] GET /api/queue: %v", err)
				writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
				return
			}
			json.NewEncoder(w).Encode(summary)
		})

		r.Get("/api/stats/timeline", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			stats, err := dbConn.GetTimelineStats(getUserID(r), isAdmin(r))
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

		// GET /api/export?format=json|csv|ndjson|xlsx|pdf|graphml
		r.Get("/api/export", func(w http.ResponseWriter, r *http.Request) {
			uid := getUserID(r)
			userSemAny, _ := exportPerUser.LoadOrStore(uid, make(chan struct{}, 1))
			userSem := userSemAny.(chan struct{})
			select {
			case userSem <- struct{}{}:
				defer func() { <-userSem }()
			default:
				writeJSONError(w, http.StatusTooManyRequests, "Ai deja un export in desfasurare — asteapta sa se termine")
				return
			}
			select {
			case exportGlobalSem <- struct{}{}:
				defer func() { <-exportGlobalSem }()
			default:
				writeJSONError(w, http.StatusTooManyRequests, "Prea multe exporturi simultane pe server — incearca din nou in cateva momente")
				return
			}
			format := r.URL.Query().Get("format")
			switch format {
			case "csv", "ndjson", "xlsx", "pdf", "graphml":
			default:
				format = "json"
			}
			rc := http.NewResponseController(w)
			rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))
			switch format {
			case "json":
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte("["))
				first := true
				err := dbConn.ExportNodes(r.Context(), getUserID(r), isAdmin(r), func(n database.Node) error {
					b, marshalErr := json.Marshal(n)
					if marshalErr != nil {
						return marshalErr
					}
					if !first {
						w.Write([]byte(","))
					}
					first = false
					_, err := w.Write(b)
					return err
				})
				w.Write([]byte("]"))
				if err != nil {
					log.Printf("[EXPORT] JSON error: %v", err)
				}
			case "ndjson":
				w.Header().Set("Content-Type", "application/x-ndjson")
				w.Header().Set("Content-Disposition", "attachment; filename=onion_spider_export.ndjson")
				enc := json.NewEncoder(w)
				err := dbConn.ExportNodes(r.Context(), getUserID(r), isAdmin(r), func(n database.Node) error { return enc.Encode(n) })
				if err != nil {
					log.Printf("[EXPORT] NDJSON error: %v", err)
				}
			case "csv":
				w.Header().Set("Content-Type", "text/csv")
				w.Header().Set("Content-Disposition", "attachment; filename=onion_spider_export.csv")
				cw := csv.NewWriter(w)
				cw.Write([]string{"id", "url", "title", "status_code", "server_header", "processing_status", "category", "last_crawled_at"})
				err := dbConn.ExportNodes(r.Context(), getUserID(r), isAdmin(r), func(n database.Node) error {
					return cw.Write([]string{
						strconv.Itoa(n.ID), sanitizeCSVField(n.URL), sanitizeCSVField(n.Title),
						strconv.Itoa(n.StatusCode), sanitizeCSVField(n.ServerHeader),
						n.ProcessingStatus, n.Category, n.LastCrawledAt,
					})
				})
				cw.Flush()
				if err != nil {
					log.Printf("[EXPORT] CSV error: %v", err)
				}
			case "xlsx":
				const xlsxRowCap = 10_000
				xf := excelize.NewFile()
				defer xf.Close()
				sheet := "Nodes"
				xf.SetSheetName("Sheet1", sheet)
				for col, h := range []string{"id", "url", "title", "status_code", "server_header", "processing_status", "category", "last_crawled_at"} {
					cell, _ := excelize.CoordinatesToCellName(col+1, 1)
					xf.SetCellValue(sheet, cell, h)
				}
				xlsxRow := 2
				err := dbConn.ExportNodes(r.Context(), getUserID(r), isAdmin(r), func(n database.Node) error {
					if xlsxRow-1 > xlsxRowCap {
						return nil
					}
					for col, v := range []interface{}{n.ID, sanitizeCSVField(n.URL), sanitizeCSVField(n.Title), n.StatusCode, sanitizeCSVField(n.ServerHeader), n.ProcessingStatus, n.Category, n.LastCrawledAt} {
						cell, _ := excelize.CoordinatesToCellName(col+1, xlsxRow)
						xf.SetCellValue(sheet, cell, v)
					}
					xlsxRow++
					return nil
				})
				if err != nil {
					log.Printf("[EXPORT] XLSX error: %v", err)
				}
				var xlsxBuf bytes.Buffer
				if err := xf.Write(&xlsxBuf); err != nil {
					writeJSONError(w, http.StatusInternalServerError, "Eroare la generarea XLSX")
					return
				}
				w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
				w.Header().Set("Content-Disposition", "attachment; filename=onion_spider_export.xlsx")
				w.Write(xlsxBuf.Bytes())
			case "pdf":
				const pdfRowCap = 5_000
				pf := gofpdf.New("L", "mm", "A4", "")
				pf.AddPage()
				pf.SetTitle("Onion Spider Export", false)
				pf.SetFont("Helvetica", "B", 8)
				type pdfCol struct {
					name  string
					width float64
				}
				cols := []pdfCol{{"ID", 12}, {"URL", 110}, {"Title", 50}, {"Status", 14}, {"Category", 28}, {"Last Crawled", 35}}
				pf.SetFillColor(50, 50, 50)
				pf.SetTextColor(255, 255, 255)
				for _, c := range cols {
					pf.CellFormat(c.width, 7, c.name, "1", 0, "C", true, 0, "")
				}
				pf.Ln(-1)
				pf.SetFont("Helvetica", "", 7)
				pf.SetTextColor(0, 0, 0)
				pdfRows := 0
				fillRow := false
				trunc := func(s string, max int) string {
					runes := []rune(s)
					if len(runes) > max {
						return string(runes[:max-1]) + "..."
					}
					return s
				}
				err := dbConn.ExportNodes(r.Context(), getUserID(r), isAdmin(r), func(n database.Node) error {
					if pdfRows >= pdfRowCap {
						return nil
					}
					if fillRow {
						pf.SetFillColor(240, 240, 240)
					} else {
						pf.SetFillColor(255, 255, 255)
					}
					pf.CellFormat(cols[0].width, 6, strconv.Itoa(n.ID), "1", 0, "R", true, 0, "")
					pf.CellFormat(cols[1].width, 6, trunc(n.URL, 100), "1", 0, "L", true, 0, "")
					pf.CellFormat(cols[2].width, 6, trunc(n.Title, 40), "1", 0, "L", true, 0, "")
					pf.CellFormat(cols[3].width, 6, strconv.Itoa(n.StatusCode), "1", 0, "C", true, 0, "")
					pf.CellFormat(cols[4].width, 6, n.Category, "1", 0, "L", true, 0, "")
					pf.CellFormat(cols[5].width, 6, n.LastCrawledAt, "1", 1, "L", true, 0, "")
					pdfRows++
					return nil
				})
				if err != nil {
					log.Printf("[EXPORT] PDF error: %v", err)
				}
				var pdfBuf bytes.Buffer
				if err := pf.Output(&pdfBuf); err != nil {
					writeJSONError(w, http.StatusInternalServerError, "Eroare la generarea PDF")
					return
				}
				w.Header().Set("Content-Type", "application/pdf")
				w.Header().Set("Content-Disposition", "attachment; filename=onion_spider_export.pdf")
				w.Write(pdfBuf.Bytes())
			case "graphml":
				w.Header().Set("Content-Type", "application/xml")
				w.Header().Set("Content-Disposition", "attachment; filename=onion_spider_export.graphml")
				fmt.Fprint(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
				fmt.Fprint(w, "<graphml xmlns=\"http://graphml.graphdrawing.org/graphml\">\n")
				fmt.Fprint(w, "  <key id=\"d0\" for=\"node\" attr.name=\"url\" attr.type=\"string\"/>\n")
				fmt.Fprint(w, "  <key id=\"d1\" for=\"node\" attr.name=\"title\" attr.type=\"string\"/>\n")
				fmt.Fprint(w, "  <key id=\"d2\" for=\"node\" attr.name=\"category\" attr.type=\"string\"/>\n")
				fmt.Fprint(w, "  <graph id=\"G\" edgedefault=\"directed\">\n")
				xmlEsc := func(s string) string {
					var sb strings.Builder
					xml.EscapeText(&sb, []byte(s))
					return sb.String()
				}
				err := dbConn.ExportNodes(r.Context(), getUserID(r), isAdmin(r), func(n database.Node) error {
					fmt.Fprintf(w, "    <node id=\"n%d\">\n", n.ID)
					fmt.Fprintf(w, "      <data key=\"d0\">%s</data>\n", xmlEsc(n.URL))
					fmt.Fprintf(w, "      <data key=\"d1\">%s</data>\n", xmlEsc(n.Title))
					fmt.Fprintf(w, "      <data key=\"d2\">%s</data>\n", xmlEsc(n.Category))
					fmt.Fprint(w, "    </node>\n")
					return nil
				})
				if err != nil {
					log.Printf("[EXPORT] GraphML nodes error: %v", err)
				}
				err = dbConn.ExportGraphMLEdges(r.Context(), getUserID(r), isAdmin(r), func(ge database.GraphMLEdge) error {
					fmt.Fprintf(w, "    <edge source=\"n%d\" target=\"n%d\"/>\n", ge.SourceID, ge.TargetID)
					return nil
				})
				if err != nil {
					log.Printf("[EXPORT] GraphML edges error: %v", err)
				}
				fmt.Fprint(w, "  </graph>\n</graphml>\n")
			}
		})
	})

	// =========================================================================
	// ADMIN-ONLY ENDPOINTS (blacklist management)
	// =========================================================================

	r.Group(func(r chi.Router) {
		r.Use(requireAuth)
		r.Use(loadDBRole(dbConn))
		r.Use(requireAdminDB)

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

		r.Post("/api/blacklist", func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Domain string `json:"domain"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 512)
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "Body invalid")
				return
			}
			req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
			if req.Domain == "" {
				writeJSONError(w, http.StatusBadRequest, "Campul 'domain' este obligatoriu")
				return
			}
			if !strings.HasSuffix(req.Domain, ".onion") {
				writeJSONError(w, http.StatusBadRequest, "Doar domeniile .onion pot fi blocate")
				return
			}
			if err := dbConn.AddBlacklist(req.Domain); err != nil {
				log.Printf("[ERROR] POST /api/blacklist domain=%s: %v", sanitizeForLog(req.Domain), err)
				writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
				return
			}
			log.Printf("[AUDIT] blacklist_add admin_user=%d domain=%s", getUserID(r), sanitizeForLog(req.Domain))
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Domeniu blocat: %s", req.Domain)})
		})

		r.Delete("/api/blacklist/{domain}", func(w http.ResponseWriter, r *http.Request) {
			domain := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "domain")))
			if domain == "" || !strings.HasSuffix(domain, ".onion") {
				writeJSONError(w, http.StatusBadRequest, "Domeniu invalid: trebuie sa fie un domeniu .onion")
				return
			}
			found, err := dbConn.DeleteBlacklist(domain)
			if err != nil {
				log.Printf("[ERROR] DELETE /api/blacklist domain=%s: %v", sanitizeForLog(domain), err)
				writeJSONError(w, http.StatusInternalServerError, "Eroare interna")
				return
			}
			if !found {
				writeJSONError(w, http.StatusNotFound, "Domeniu negasit in blacklist")
				return
			}
			log.Printf("[AUDIT] blacklist_remove admin_user=%d domain=%s", getUserID(r), sanitizeForLog(domain))
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Domeniu scos din blacklist: %s", domain)})
		})
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

// sanitizeCSVField previne formula injection in CSV/XLSX.
func sanitizeCSVField(s string) string {
	if len(s) > 0 {
		switch s[0] {
		case '=', '+', '-', '@', '\t', '\r':
			return "'" + s
		}
	}
	return s
}

// safeLogger inlocuieste middleware.Logger standard: loghează request-urile dar
// OMITE query string-ul pentru path-urile sensibile (ex: /api/auth/verify care
// primeste token in URL — ar ajunge in syslog/journald).
func safeLogger(sensitivePaths ...string) func(http.Handler) http.Handler {
	sensitive := make(map[string]struct{}, len(sensitivePaths))
	for _, p := range sensitivePaths {
		sensitive[p] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				uri := r.URL.Path
				if r.URL.RawQuery != "" {
					if _, isSensitive := sensitive[r.URL.Path]; !isSensitive {
						uri = r.URL.Path + "?" + r.URL.RawQuery
					} else {
						uri = r.URL.Path + "?[REDACTED]"
					}
				}
				log.Printf("%s %s %d %dB %s from %s",
					r.Method, uri, ww.Status(), ww.BytesWritten(),
					time.Since(start).Round(time.Millisecond), sanitizeForLog(clientIP(r)))
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

// validatePassword impune minim 10 caractere, max 72 (limita bcrypt),
// si cere diversitate (cel putin 3 din: litera mica, litera mare, cifra, simbol).
// Previne parole triviale gen "passwordaa" sau "aaaaaaaaaa".
func validatePassword(p string) error {
	if len(p) < 10 || len(p) > 72 {
		return errors.New("Parola trebuie sa aiba intre 10 si 72 caractere")
	}
	var hasLower, hasUpper, hasDigit, hasSymbol bool
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= '0' && r <= '9':
			hasDigit = true
		case r > 32 && r < 127:
			hasSymbol = true
		}
	}
	classes := 0
	for _, ok := range []bool{hasLower, hasUpper, hasDigit, hasSymbol} {
		if ok {
			classes++
		}
	}
	if classes < 3 {
		return errors.New("Parola trebuie sa combine cel putin 3 categorii: litere mici, litere mari, cifre, simboluri")
	}
	return nil
}

// sanitizeForLog previne log injection prin newline-uri.
func sanitizeForLog(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}
