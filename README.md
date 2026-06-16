# 🕸️ Onion Spider

A dark web crawler built for performance, concurrency, and safety. Explores the Tor network by extracting information and links between `.onion` sites — without executing any dangerous code.

## 🏗️ Architecture

Two main components:

1. **Backend (Go + PostgreSQL)**
   - **API Server:** Exposes collected data via a REST API consumed by the web interface.
   - **Crawler Engine:** A concurrent worker-pool system that routes all HTTP traffic through a SOCKS5 proxy (Tor) to download and scrape raw HTML from `.onion` sites.
   - **Database:** PostgreSQL stores discovered nodes, metadata, content hashes, categories, and the link graph between sites.

2. **Frontend (Vue 3 + Vite)**
   - A statically compiled interactive dashboard served via Nginx.
   - Provides real-time visibility into crawler status, discovered sites, and an interactive network graph.

## 🛡️ Safety Principles (Sandbox)

To protect the server and ensure passive browsing:
- **Traffic isolation:** All external requests go exclusively through the Tor SOCKS5 proxy.
- **No JavaScript execution:** The crawler only reads `text/html` HTTP responses. No headless browsers.
- **No media downloads:** Images, archives, and executables are ignored.

## 🔒 Security Features

- JWT (HS256) authentication with a user/admin role system. The signing
  algorithm is pinned, so forged `alg=none` tokens are rejected.
- Authorization reads the role from the database on every request, not from the
  JWT claims — a demoted admin loses access immediately instead of waiting for
  the token to expire.
- Passwords hashed with bcrypt (cost 12). Login runs a constant-time comparison
  (and a dummy hash for unknown emails) to prevent account enumeration via timing.
- Account lockout after repeated failed logins, plus per-IP and per-recipient
  rate limiting — all auth events are written to an audit log.
- Email verification uses a POST-confirmation page so link-preview bots can't
  silently activate accounts; recipient addresses are parsed with
  `net/mail.ParseAddress` and CRLF-stripped to block header injection.
- Crawler SSRF defense: requests go only to `.onion` hosts over Tor, redirects
  to clearnet or other onion domains are blocked, and redirect depth is capped.
- Formula-injection prevention in CSV/XLSX exports; XML escaping in GraphML.
- All request bodies are size-capped (`MaxBytesReader`) with unknown-field
  rejection; the HTTP server binds to `127.0.0.1` only, behind an nginx reverse
  proxy that adds HSTS, CSP and the rest of the security headers.

## 🚀 Tech Stack

- **Backend:** Go (Golang)
- **Frontend:** JavaScript / Vue 3 (Composition API)
- **Database:** PostgreSQL
- **Web Server:** Nginx
- **Router:** `go-chi/chi`
- **HTML Scraping:** `goquery`
- **Graph Visualization:** `vis-network`
- **Exports:** CSV, JSON, NDJSON, XLSX, PDF, GraphML

## 🛠️ Directory Structure

```text
onion-spider/
├── backend/
│   ├── cmd/
│   │   ├── api/              # REST API entry point
│   │   └── crawler/          # Standalone crawler entry point
│   └── internal/
│       ├── auth/             # JWT authentication
│       ├── crawler/          # Engine, scraper, robots.txt, sitemap, categorizer
│       ├── database/         # PostgreSQL connection and queries
│       ├── email/            # Email verification
│       └── proxy/            # Tor SOCKS5 client + circuit controller
├── frontend/
│   ├── public/
│   ├── src/
│   │   ├── App.vue           # Main application component
│   │   └── main.js
│   ├── package.json
│   └── vite.config.js
└── README.md
```

## ⚙️ Running Locally (Development)

**1. Database**
Install PostgreSQL and create a database (e.g. `onion_spider`). Set `DATABASE_URL` in `backend/.env`.

**2. Start the API (Go)**
```bash
cd backend
cp .env.example .env   # fill in your values
go run ./cmd/api/main.go
# Server starts on port 8900 by default
```

**3. Start the Frontend (Vue)**
```bash
cd frontend
npm install
npm run dev
# Dashboard available at http://localhost:5173
```

## 🌐 Production Deployment (Nginx)

The project is configured to run in production with Nginx as a reverse proxy:
- Serves static files from `frontend/dist/` on ports 80/443.
- Proxies `/api/*` requests to the Go binary running on port `8900`.
