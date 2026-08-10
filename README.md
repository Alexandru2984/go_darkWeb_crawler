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
- The browser session lives in an `HttpOnly`, `Secure`, `SameSite=Strict`
  cookie — the dashboard never puts a token in `localStorage`, so an XSS has no
  long-lived credential to exfiltrate. Login returns a JWT only when a client
  explicitly requests bearer mode, which does not set ambient cookies.
  Cookie-authenticated writes must echo a double-submit CSRF token;
  `Authorization: Bearer` still works for scripts and is exempt, since a browser
  cannot attach that header cross-origin.
- All crawler traffic uses per-destination Tor stream isolation: each onion host
  gets its own SOCKS credential, so Tor builds a separate circuit per site and no
  single relay sees one client reading two unrelated services.
- Authorization reads the role from the database on every request, not from the
  JWT claims — a demoted admin loses access immediately instead of waiting for
  the token to expire.
- Passwords hashed with Argon2id (t=3, 64 MiB, p=4, RFC 9106's second
  recommended configuration), stored as PHC strings that carry their own
  parameters. Accounts predating the migration keep their bcrypt hash and are
  upgraded in place on their next successful login, without a reset email and
  without signing their other sessions out. Login runs a constant-time comparison
  (and a dummy hash for unknown emails) to prevent account enumeration via timing.
- Each login gets its own server-side session, so a single device can be signed
  out without disturbing the others — `token_version` could only ever revoke
  everything at once. Sessions store a coarse device family ("Firefox on Linux")
  and never an IP address or the raw User-Agent. Revocation takes effect on the
  device's next request, and logging out revokes the session rather than merely
  clearing the cookie, so a copied token stops working too.
- Two-factor authentication (TOTP, RFC 6238) with ten single-use recovery
  codes. Only code digests are stored. Each accepted code advances a per-account
  watermark, so a code observed in transit cannot be replayed inside its
  validity window. Administrative endpoints refuse to act for an admin without a
  second factor enrolled — enrolment itself is never gated, so the requirement
  can always be satisfied. Disabling it demands both the password and a current
  code, so a stolen session cannot strip the control meant to outlast it.
- Account lockout after repeated failed logins, plus per-recipient rate limiting
  — all auth events are written to an audit log. Authenticated requests are
  rate-limited per account rather than per address, because the onion vhost has
  no client address to report and every Tor visitor would otherwise share one
  budget; pre-authentication limits fall back to the address, namespaced by
  front door.
- Email verification uses a POST-confirmation page so link-preview bots can't
  silently activate accounts; recipient addresses are parsed with
  `net/mail.ParseAddress` and CRLF-stripped to block header injection. SMTP has
  a bounded end-to-end timeout and refuses to send credentials or messages
  unless the server establishes certificate-verified TLS 1.2 or newer.
- Crawler SSRF defense: requests go only to `.onion` hosts over Tor, redirects
  to clearnet or other onion domains are blocked, and redirect depth is capped.
  V3 addresses are verified against Tor's embedded version and SHA3 checksum;
  userinfo and non-web ports are rejected before a URL can enter the queue.
- Formula-injection prevention in CSV/XLSX exports; XML escaping in GraphML.
- All request bodies are size-capped (`MaxBytesReader`) with unknown-field
  rejection; the HTTP server binds to `127.0.0.1` only, behind an nginx reverse
  proxy that adds HSTS, CSP and the rest of the security headers.

## 📚 API

The full REST API is documented as an OpenAPI 3.0 spec at
[`docs/openapi.yaml`](docs/openapi.yaml) — endpoints, request/response schemas,
auth scheme and error codes. Paste it into any Swagger/Redoc viewer to browse
it interactively.

Security and production planning:

- [`docs/security-threat-model.md`](docs/security-threat-model.md)
- [`docs/incident-response.md`](docs/incident-response.md)
- [`docs/production-roadmap.md`](docs/production-roadmap.md)

## 📱 Interface

The dashboard is a routed Vue 3 application, mobile-first from 320 px:

- **Routes are code-split.** `vis-network` is roughly three quarters of the old
  bundle and used to be downloaded by everyone who merely opened the page. It
  now lives in a chunk loaded only when a signed-in user opens the network map,
  which took the login route from ~187 kB gzip to ~43 kB — the difference is
  felt most over Tor.
- **The dense table becomes cards below 900 px** rather than scrolling sideways
  or dropping columns; every row keeps its category, status and response code.
- **Controls are built to a 44 px touch target**, inputs stay at 16 px so iOS
  does not zoom the page on focus, and the navigation collapses into a drawer.
- **Accessibility:** landmarks, a skip link, real labels bound to every field,
  `role="alert"` on feedback, a live region for crawler status, visible focus
  rings, and a stated text equivalent for the graph canvas (the list view),
  which is not keyboard-operable.
- **Account security screen** for enrolling two-factor authentication, storing
  recovery codes, and reviewing or signing out individual devices.

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
