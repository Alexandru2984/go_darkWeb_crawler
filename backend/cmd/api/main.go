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

// jwtMiddleware extracts claims from the Authorization header if present and valid.
// No header: pass-through (public endpoints work).
// Header present but invalid: 401 (do not allow forged tokens to pass as unauthenticated).
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
			writeJSONError(w, http.StatusUnauthorized, "Invalid or expired token")
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Value(userContextKey).(*auth.Claims); !ok {
			writeJSONError(w, http.StatusUnauthorized, "Authentication required")
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
				writeJSONError(w, http.StatusInternalServerError, "Internal error")
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
			writeJSONError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		if !isAdmin(r) {
			writeJSONError(w, http.StatusForbidden, "Admin role required")
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
		log.Println("⚠️  No .env file found, using system environment variables")
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("FATAL: DATABASE_URL is missing")
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
	corsOrigins := splitAndTrim(corsOrigin, ",")

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(safeLogger("/api/auth/verify"))
	r.Use(middleware.Recoverer)
	r.Use(jwtMiddleware)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: corsOrigins,
		AllowedMethods: []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Content-Type", "Authorization"},
		MaxAge:         300,
	}))

	// Rate limiters: separate per operation type.
	crawlLim := newCrawlLimiter(20, time.Minute)      // /api/crawl*, /api/recrawl
	searchLim := newCrawlLimiter(60, time.Minute)     // /api/search
	loginLim := newCrawlLimiter(5, time.Minute)       // /api/auth/login
	registerLim := newCrawlLimiter(3, time.Hour)      // /api/auth/register
	verifyLim := newCrawlLimiter(10, time.Minute)     // /api/auth/verify

	dbConn, err := database.InitDB(dsn)
	if err != nil {
		log.Fatalf("FATAL: DB connection: %v", err)
	}

	// =========================================================================
	// AUTH ENDPOINTS (public, rate-limited, audit-logged)
	// =========================================================================

	r.Post("/api/auth/register", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !registerLim.allow(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "Too many registrations from this IP. Please try again later.")
			return
		}
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid data")
			return
		}
		req.Email = database.NormalizeEmail(req.Email)
		if !emailRegex.MatchString(req.Email) || len(req.Email) > 254 {
			writeJSONError(w, http.StatusBadRequest, "Invalid email")
			return
		}
		if err := validatePassword(req.Password); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Rate-limit per recipient address — protects Gmail quota from abuse.
		// Max 3 register attempts per email per hour.
		if n, err := dbConn.CountRecentAuthEvents("register_ok", req.Email, 60); err == nil && n >= 3 {
			log.Printf("[AUDIT] register_blocked ip=%s email=%s count=%d", sanitizeForLog(ip), sanitizeForLog(req.Email), n)
			writeJSONError(w, http.StatusTooManyRequests, "This email has already received too many verification emails. Try again in an hour.")
			return
		}

		// Default role: user. Admin bootstrap is controlled via ADMIN_EMAIL
		// and is only allowed if no admin exists yet in the system.
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
			writeJSONError(w, http.StatusBadRequest, "Password cannot be processed")
			return
		}
		token := auth.GenerateVerificationToken()

		if err := dbConn.CreateUser(req.Email, hash, role, token); err != nil {
			log.Printf("[AUDIT] register_fail ip=%s email=%s: %v", sanitizeForLog(ip), sanitizeForLog(req.Email), err)
			dbConn.LogAuthEvent("register_fail", req.Email, ip)
			writeJSONError(w, http.StatusBadRequest, "Error: email already in use or invalid data")
			return
		}

		dbConn.LogAuthEvent("register_ok", req.Email, ip)
		log.Printf("[AUDIT] register_ok ip=%s email=%s role=%s", sanitizeForLog(ip), sanitizeForLog(req.Email), role)

		go func() {
			if err := email.SendVerificationEmail(req.Email, token); err != nil {
				log.Printf("[email] send error to %s: %v", sanitizeForLog(req.Email), err)
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"message": "Account created! Please check your email."})
	})

	r.Post("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !loginLim.allow(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "Too many login attempts. Please try again in 1 minute.")
			return
		}
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid data")
			return
		}
		req.Email = database.NormalizeEmail(req.Email)
		if req.Email == "" || req.Password == "" {
			writeJSONError(w, http.StatusBadRequest, "Email and password are required")
			return
		}

		// Account lockout: after 5 login_fail in 15min for the same email → 15min timeout.
		// Protects against distributed brute-force across multiple IPs.
		if n, err := dbConn.CountRecentAuthEvents("login_fail", req.Email, 15); err == nil && n >= 5 {
			// Ruleaza bcrypt oricum pentru a mentine timpul constant (evita detectia lockout-ului).
			auth.CheckAgainstDummy(req.Password)
			dbConn.LogAuthEvent("login_locked", req.Email, ip)
			log.Printf("[AUDIT] login_locked ip=%s email=%s count=%d", sanitizeForLog(ip), sanitizeForLog(req.Email), n)
			writeJSONError(w, http.StatusTooManyRequests, "Account temporarily locked due to too many failed attempts. Wait 15 minutes.")
			return
		}

		user, err := dbConn.GetUserByEmail(req.Email)
		if err != nil {
			log.Printf("[ERROR] GetUserByEmail: %v", err)
			// Rulam bcrypt ca sa pastram timpul constant chiar si pe eroare DB.
			auth.CheckAgainstDummy(req.Password)
			writeJSONError(w, http.StatusInternalServerError, "Internal error")
			return
		}
		// TIMING ATTACK MITIGATION: chiar si pe user inexistent, rulam bcrypt
		// pe un hash dummy. Fara asta, diferenta de timp (700ms vs 100ms) permite
		// enumerarea emailurilor inregistrate.
		if user == nil {
			auth.CheckAgainstDummy(req.Password)
			dbConn.LogAuthEvent("login_fail", req.Email, ip)
			log.Printf("[AUDIT] login_fail ip=%s email=%s reason=unknown_user", sanitizeForLog(ip), sanitizeForLog(req.Email))
			writeJSONError(w, http.StatusUnauthorized, "Invalid credentials")
			return
		}
		if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
			dbConn.LogAuthEvent("login_fail", req.Email, ip)
			log.Printf("[AUDIT] login_fail ip=%s email=%s reason=bad_password", sanitizeForLog(ip), sanitizeForLog(req.Email))
			writeJSONError(w, http.StatusUnauthorized, "Invalid credentials")
			return
		}

		if !user.IsVerified {
			dbConn.LogAuthEvent("login_unverified", req.Email, ip)
			writeJSONError(w, http.StatusForbidden, "Account is not yet verified")
			return
		}

		token, err := auth.GenerateToken(user.ID, user.Email, user.Role)
		if err != nil {
			log.Printf("[ERROR] JWT generation for %s: %v", sanitizeForLog(user.Email), err)
			writeJSONError(w, http.StatusInternalServerError, "Internal error")
			return
		}

		dbConn.LogAuthEvent("login_ok", req.Email, ip)
		log.Printf("[AUDIT] login_ok ip=%s email=%s role=%s", sanitizeForLog(ip), sanitizeForLog(user.Email), user.Role)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": token, "role": user.Role, "email": user.Email})
	})

	// GET /api/auth/verify?token=X — afiseaza doar o pagina de confirmare cu un buton POST.
	// NU consuma tokenul. Protejeaza impotriva link-preview bots (Outlook/Gmail/Slack)
	// care dau GET pe link si ar auto-verifica contul in absenta userului.
	r.Get("/api/auth/verify", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !verifyLim.allow(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "Too many attempts. Please try again in 1 minute.")
			return
		}
		token := r.URL.Query().Get("token")
		if len(token) < 16 || len(token) > 128 {
			writeJSONError(w, http.StatusBadRequest, "Invalid token")
			return
		}
		// Nu trimitem tokenul direct in pagina ca plaintext — il punem intr-un input hidden
		// dupa ce verificam ca are doar caractere safe pentru HTML (hex/base64 URL-safe).
		if !tokenSafeRE.MatchString(token) {
			writeJSONError(w, http.StatusBadRequest, "Invalid token")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Account confirmation</title>
<meta name="robots" content="noindex,nofollow"><meta name="referrer" content="no-referrer"></head>
<body style="font-family:sans-serif;max-width:480px;margin:4rem auto;text-align:center">
<h1>Confirm account activation</h1>
<p>Click the button below to complete email verification.</p>
<form method="POST" action="/api/auth/verify">
<input type="hidden" name="token" value="%s">
<button type="submit" style="padding:0.75rem 1.5rem;font-size:1rem;cursor:pointer">Confirm</button>
</form></body></html>`, token)
	})

	// POST /api/auth/verify — actually consumes the token (accepts JSON body or form-encoded).
	r.Post("/api/auth/verify", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !verifyLim.allow(ip) {
			writeJSONError(w, http.StatusTooManyRequests, "Too many attempts. Please try again in 1 minute.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		var token string
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "application/json") {
			var req struct {
				Token string `json:"token"`
			}
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid body")
				return
			}
			token = req.Token
		} else {
			if err := r.ParseForm(); err != nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid form")
				return
			}
			token = r.PostFormValue("token")
		}
		if len(token) < 16 || len(token) > 128 || !tokenSafeRE.MatchString(token) {
			writeJSONError(w, http.StatusBadRequest, "Invalid token")
			return
		}
		if err := dbConn.VerifyUser(token); err != nil {
			log.Printf("[AUDIT] verify_fail ip=%s: %v", sanitizeForLog(ip), err)
			writeJSONError(w, http.StatusBadRequest, "Token invalid, expired or already used")
			return
		}
		log.Printf("[AUDIT] verify_ok ip=%s", sanitizeForLog(ip))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>Account verified</title></head><body style="font-family:sans-serif;max-width:480px;margin:4rem auto;text-align:center"><h1>Account successfully verified!</h1><p><a href="/">Back to login</a></p></body></html>`))
	})

	// =========================================================================
	// STATUS (public — only shows the server is running; private data requires auth)
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
		log.Printf("⚠️  TorController: control port unavailable, circuit renewal disabled: %v", err)
	} else {
		engine.TorCtrl = torCtrl
		log.Println("✅ TorController active")
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
				log.Printf("[sweeper] ResetStuckCrawling error: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("[sweeper] recovered %d nodes stuck in 'crawling'", n)
			}
		}
	}()

	// Retention auth_audit: sterge entry-urile mai vechi de 90 zile.
	// Rezolva GDPR (PII = email) + crestere nelimitata a tabelei. Ruleaza la pornire + la 24h.
	auditRetention := 90 * 24 * time.Hour
	if v := os.Getenv("AUDIT_RETENTION_DAYS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			auditRetention = time.Duration(d) * 24 * time.Hour
		}
	}
	go func() {
		run := func() {
			n, err := dbConn.PurgeOldAuditLogs(auditRetention)
			if err != nil {
				log.Printf("[retention] PurgeOldAuditLogs error: %v", err)
				return
			}
			if n > 0 {
				log.Printf("[retention] deleted %d auth_audit entries older than %v", n, auditRetention)
			}
		}
		run()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			run()
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
				writeJSONError(w, http.StatusInternalServerError, "Internal error")
				return
			}
			json.NewEncoder(w).Encode(nodes)
		})

		r.Get("/api/node", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			nodeURL := r.URL.Query().Get("url")
			if nodeURL == "" {
				writeJSONError(w, http.StatusBadRequest, "Parameter 'url' is required")
				return
			}
			node, err := dbConn.GetNodeByURL(nodeURL, getUserID(r), isAdmin(r))
			if err != nil {
				log.Printf("[ERROR] GET /api/node url=%s: %v", sanitizeForLog(nodeURL), err)
				writeJSONError(w, http.StatusInternalServerError, "Internal error")
				return
			}
			if node == nil {
				writeJSONError(w, http.StatusNotFound, "Node not found")
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
				writeJSONError(w, http.StatusInternalServerError, "Internal error")
				return
			}
			json.NewEncoder(w).Encode(edges)
		})

		r.Get("/api/search", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			ip := clientIP(r)
			if !searchLim.allow(ip) {
				writeJSONError(w, http.StatusTooManyRequests, "Rate limit exceeded — max 60 searches/minute")
				return
			}
			q := r.URL.Query().Get("q")
			if q == "" {
				writeJSONError(w, http.StatusBadRequest, "Parameter 'q' is required")
				return
			}
			if len(q) > 200 {
				writeJSONError(w, http.StatusBadRequest, "Query too long (max 200 characters)")
				return
			}
			category := r.URL.Query().Get("category")
			nodes, err := dbConn.SearchNodes(q, category, getUserID(r), isAdmin(r))
			if err != nil {
				log.Printf("[ERROR] GET /api/search q=%s: %v", sanitizeForLog(q), err)
				writeJSONError(w, http.StatusInternalServerError, "Internal error")
				return
			}
			json.NewEncoder(w).Encode(nodes)
		})

		r.Post("/api/crawl", func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !isAdmin(r) && !crawlLim.allow(ip) {
				writeJSONError(w, http.StatusTooManyRequests, "Too many requests. Please try again in a few minutes.")
				return
			}
			var req struct {
				URL string `json:"url"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 2048)
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid body")
				return
			}
			req.URL = normalizeOnionURL(req.URL)
			if !isValidOnionURL(req.URL) {
				writeJSONError(w, http.StatusBadRequest, "Invalid URL: must be a valid .onion v3 URL (http/https)")
				return
			}
			log.Printf("[AUDIT] POST /api/crawl ip=%s user=%d url=%s", sanitizeForLog(ip), getUserID(r), sanitizeForLog(req.URL))
			if err := engine.AddToQueue(req.URL, getUserID(r)); err != nil {
				if errors.Is(err, database.ErrBlacklisted) {
					writeJSONError(w, http.StatusForbidden, "Domain blocked")
					return
				}
				log.Printf("[ERROR] POST /api/crawl user=%d url=%s: %v", getUserID(r), sanitizeForLog(req.URL), err)
				writeJSONError(w, http.StatusInternalServerError, "Internal error")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"message": "URL added to the crawling queue"})
		})

		r.Post("/api/recrawl", func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !isAdmin(r) && !crawlLim.allow(ip) {
				writeJSONError(w, http.StatusTooManyRequests, "Too many requests. Please try again in a few minutes.")
				return
			}
			var req struct {
				URL string `json:"url"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 2048)
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid body")
				return
			}
			req.URL = normalizeOnionURL(req.URL)
			if !isValidOnionURL(req.URL) {
				writeJSONError(w, http.StatusBadRequest, "Invalid URL")
				return
			}
			found, canRequeue, err := dbConn.RequeueForCrawl(req.URL, getUserID(r))
			if err != nil {
				log.Printf("[ERROR] POST /api/recrawl url=%s: %v", sanitizeForLog(req.URL), err)
				writeJSONError(w, http.StatusInternalServerError, "Internal error")
				return
			}
			if !found {
				writeJSONError(w, http.StatusNotFound, "URL does not exist in the database")
				return
			}
			if !canRequeue {
				writeJSONError(w, http.StatusConflict, "Node is already being crawled")
				return
			}
			log.Printf("[AUDIT] POST /api/recrawl ip=%s url=%s", ip, sanitizeForLog(req.URL))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"message": "Node has been queued for re-crawling"})
		})

		r.Post("/api/crawl/bulk", func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !isAdmin(r) && !crawlLim.allow(ip) {
				writeJSONError(w, http.StatusTooManyRequests, "Too many requests. Please try again in a few minutes.")
				return
			}
			var req struct {
				URLs []string `json:"urls"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 10*1024)
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid body")
				return
			}
			if len(req.URLs) == 0 || len(req.URLs) > 20 {
				writeJSONError(w, http.StatusBadRequest, "Send 1-20 URLs in the 'urls' field")
				return
			}
			var added, skipped int
			for _, u := range req.URLs {
				u = normalizeOnionURL(u)
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
				writeJSONError(w, http.StatusInternalServerError, "Internal error")
				return
			}
			json.NewEncoder(w).Encode(summary)
		})

		r.Get("/api/stats/timeline", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			stats, err := dbConn.GetTimelineStats(getUserID(r), isAdmin(r))
			if err != nil {
				log.Printf("[ERROR] GET /api/stats/timeline: %v", err)
				writeJSONError(w, http.StatusInternalServerError, "Internal error")
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
				writeJSONError(w, http.StatusTooManyRequests, "You already have an export in progress — wait for it to finish")
				return
			}
			select {
			case exportGlobalSem <- struct{}{}:
				defer func() { <-exportGlobalSem }()
			default:
				writeJSONError(w, http.StatusTooManyRequests, "Too many simultaneous exports on the server — try again in a few moments")
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
					writeJSONError(w, http.StatusInternalServerError, "Error generating XLSX")
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
					fillRow = !fillRow
					return nil
				})
				if err != nil {
					log.Printf("[EXPORT] PDF error: %v", err)
				}
				var pdfBuf bytes.Buffer
				if err := pf.Output(&pdfBuf); err != nil {
					writeJSONError(w, http.StatusInternalServerError, "Error generating PDF")
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
				writeJSONError(w, http.StatusInternalServerError, "Internal error")
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
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid body")
				return
			}
			req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
			if req.Domain == "" {
				writeJSONError(w, http.StatusBadRequest, "The 'domain' field is required")
				return
			}
			if !strings.HasSuffix(req.Domain, ".onion") {
				writeJSONError(w, http.StatusBadRequest, "Only .onion domains can be blocked")
				return
			}
			if err := dbConn.AddBlacklist(req.Domain); err != nil {
				log.Printf("[ERROR] POST /api/blacklist domain=%s: %v", sanitizeForLog(req.Domain), err)
				writeJSONError(w, http.StatusInternalServerError, "Internal error")
				return
			}
			log.Printf("[AUDIT] blacklist_add admin_user=%d domain=%s", getUserID(r), sanitizeForLog(req.Domain))
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Domain blocked: %s", req.Domain)})
		})

		r.Delete("/api/blacklist/{domain}", func(w http.ResponseWriter, r *http.Request) {
			domain := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "domain")))
			if domain == "" || !strings.HasSuffix(domain, ".onion") {
				writeJSONError(w, http.StatusBadRequest, "Invalid domain: must be a .onion domain")
				return
			}
			found, err := dbConn.DeleteBlacklist(domain)
			if err != nil {
				log.Printf("[ERROR] DELETE /api/blacklist domain=%s: %v", sanitizeForLog(domain), err)
				writeJSONError(w, http.StatusInternalServerError, "Internal error")
				return
			}
			if !found {
				writeJSONError(w, http.StatusNotFound, "Domain not found in blacklist")
				return
			}
			log.Printf("[AUDIT] blacklist_remove admin_user=%d domain=%s", getUserID(r), sanitizeForLog(domain))
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Domain removed from blacklist: %s", domain)})
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
		log.Printf("=== [API] Server listening on port %s ===", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error starting server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Graceful shutdown initiated...")
	engine.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Error shutting down HTTP server: %v", err)
	}
	log.Println("Server stopped.")
}

// v3OnionHostRE accepta doar adrese onion v3 valide: 56 caractere base32 (a-z2-7) + ".onion"
// optional cu port (":" + cifre). v2 (16 chars) a fost deprecated de Tor.
var v3OnionHostRE = regexp.MustCompile(`^[a-z2-7]{56}\.onion(:[0-9]{1,5})?$`)

// tokenSafeRE valideaza tokene de verificare email/password-reset: hex, base64url sau alfanumeric + '-_'.
// Elimina orice risc de HTML/URL injection cand tokenul e reafisat intr-o pagina.
var tokenSafeRE = regexp.MustCompile(`^[A-Za-z0-9_\-]{16,128}$`)

func isValidOnionURL(rawURL string) bool {
	if rawURL == "" || len(rawURL) > 2048 {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Host)
	return v3OnionHostRE.MatchString(host)
}

// normalizeOnionURL produce forma canonica pentru un URL onion: scheme + host lowercase,
// path/query pastrate. Returneaza "" daca URL-ul nu e valid.
func normalizeOnionURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String()
}

// splitAndTrim imparte un string dupa separator si taie spatiile din jurul fiecarei piese,
// eliminand rezultatele goale.
func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
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

// safeLogger replaces the standard middleware.Logger: logs requests but
// OMITS the query string for sensitive paths (e.g. /api/auth/verify which
// receives the token in the URL — would end up in syslog/journald).
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
		return errors.New("Password must be between 10 and 72 characters")
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
		return errors.New("Password must combine at least 3 categories: lowercase, uppercase, digits, symbols")
	}
	return nil
}

// sanitizeForLog previne log injection prin newline-uri.
func sanitizeForLog(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}
