# 🕷️ Onion Spider

**Onion Spider** is an enterprise-grade, recursive Dark Web crawler and search engine. Built for performance and stability, it maps the hidden layers of the Tor network (`.onion` sites), extracts metadata and content, and visualizes the connections between hidden services through an interactive network graph.

## ✨ Features

*   **Recursive Crawling Engine:** Automatically extracts `.onion` links from scanned pages and adds them to a persistent, database-backed processing queue.
*   **Full-Text Search Engine:** Uses PostgreSQL's `tsvector` and GIN indexing to provide instant, highly relevant search results across all scraped content and titles.
*   **Interactive Network Graph:** Visualizes the Dark Web topology in real-time using `vis-network`. Nodes scale dynamically based on their connection degree (identifying major hubs), with organic physics and detailed tooltips.
*   **OOM (Out Of Memory) Guard:** Protects the crawler by enforcing a strict 5MB download limit per page and validating `Content-Type` headers to ignore massive binary files (e.g., ISOs, videos).
*   **Politeness & Domain Rate Limiting:** Implements intelligent delays (e.g., 5 seconds) between requests to the same domain to prevent overwhelming fragile hidden services or triggering anti-DDoS protections.
*   **The "Lazarus" Protocol (Exponential Backoff):** Handles the inherent instability of the Tor network. If a site timeouts or returns a SOCKS error, the crawler doesn't mark it as dead immediately. It schedules retries at increasing intervals (15 mins, 30 mins) before finally marking it as failed.
*   **Production-Ready:** Configured to run as a reliable Linux `systemd` service, completely detached from the terminal, with environment-based configuration (`.env`) and compound database indexes for ultra-fast queue processing.

## 🏗️ Architecture

*   **Backend:** Go (Golang) with `go-chi` for routing and `goquery` for HTML parsing.
*   **Frontend:** Vue.js 3 + Vite, featuring Vanilla CSS dark mode and `vis-network` for WebGL/Canvas graph rendering.
*   **Database:** PostgreSQL (storing nodes, edges, extracted JSONB metadata, and text content).
*   **Networking:** Local Tor proxy (SOCKS5 on port `9050`).

## 🚀 Setup & Installation

### Prerequisites
*   [Go](https://golang.org/) (1.20+)
*   [Node.js](https://nodejs.org/) & npm
*   [PostgreSQL](https://www.postgresql.org/)
*   [Tor](https://www.torproject.org/) service running locally

### 1. Database Setup
Create a PostgreSQL database and user. The tables (`nodes`, `edges`) and the required indexes will be created automatically by the Go application upon the first run.

### 2. Backend Setup
Navigate to the `backend` directory:
```bash
cd onion-spider/backend
```

Create a `.env` file based on your configuration:
```env
DATABASE_URL=postgres://your_user:your_password@localhost:5432/onion_spider?sslmode=disable
PORT=8900
TOR_PROXY=127.0.0.1:9050
WORKERS=3
```

Download dependencies and build the binary:
```bash
go mod tidy
go build -o onion-spider-api ./cmd/api
```

### 3. Frontend Setup
Navigate to the `frontend` directory:
```bash
cd ../frontend
npm install
```

To build for production (e.g., serving via Nginx):
```bash
npm run build
```
*The compiled assets will be available in the `dist/` folder.*

## ⚙️ Running the Project

### Development Mode
**Backend:**
```bash
cd backend
go run cmd/api/main.go
```
**Frontend:**
```bash
cd frontend
npm run dev
```

### Production Mode (Systemd + Nginx)
1.  Create a `systemd` service file for the Go binary to ensure it restarts automatically and survives system reboots.
2.  Configure Nginx to serve the static files from `frontend/dist` and proxy `/api` requests to your Go backend port (e.g., `8900`).

## ⚠️ Disclaimer
This tool is intended for research, cybersecurity analysis, and educational purposes. The developer is not responsible for the content indexed by the crawler or any misuse of the application. Always exercise caution and adhere to local laws when accessing the Dark Web.
