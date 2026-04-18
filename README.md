# 🕸️ Onion Spider Sandbox

Un Dark Web Crawler (Onion Spider) construit cu accent pe performanță, concurență și siguranță (sandbox). Proiectul explorează rețeaua Tor extrăgând informații și legături între site-urile `.onion`, fără a executa cod periculos.

## 🏗️ Arhitectura Proiectului

Acest proiect este împărțit în două componente principale:

1. **Backend (Go + PostgreSQL)**
   - **API Server:** Expune datele colectate printr-un REST API pentru a fi consumate de interfața web.
   - **Crawler Engine:** Un sistem concurent bazat pe *worker pools* care rutează traficul HTTP printr-un proxy SOCKS5 (Tor) pentru a descărca și analiza (scrape) codul sursă HTML brut al site-urilor `.onion`.
   - **Bază de date:** PostgreSQL este folosit pentru a stoca nodurile descoperite, metadatele și graful legăturilor dintre site-uri.

2. **Frontend (Vue 3 + Vite)**
   - Un dashboard interactiv, compilat static și servit via Nginx.
   - Oferă vizibilitate în timp real asupra statusului crawler-ului și statisticilor rețelei descoperite.

## 🛡️ Principii de Siguranță (Sandbox)

Pentru a proteja serverul și a asigura o navigare pasivă:
- **Izolare trafic:** Toate request-urile externe se fac exclusiv prin proxy-ul Tor (SOCKS5).
- **Fără execuție JavaScript:** Crawler-ul citește doar răspunsul HTTP de tip `text/html`. Nu folosește browsere headless.
- **Fără descărcări media:** Se ignoră imaginile, arhivele sau executabilele.

## 🚀 Tehnologii Folosite

- **Limbaj Backend:** Go (Golang)
- **Limbaj Frontend:** JavaScript / Vue 3 (Composition API)
- **Bază de Date:** PostgreSQL
- **Web Server / Proxy:** Nginx
- **Router Go:** `go-chi/chi`
- **Scraping Go:** `goquery` (urmează a fi implementat)

## 🛠️ Structura Directoarelor

```text
onion-spider/
├── backend/
│   ├── cmd/
│   │   ├── api/        # Punctul de intrare pentru REST API
│   │   └── crawler/    # Punctul de intrare pentru motorul de crawling
│   └── internal/
│       ├── crawler/    # Logica de rețea, SOCKS5 și parsare HTML
│       ├── database/   # Conexiunea la PostgreSQL și query-uri
│       ├── models/     # Structurile de date (Nodes, Edges)
│       └── proxy/      # Configurările pentru rețeaua Tor
├── frontend/
│   ├── public/
│   ├── src/
│   │   ├── components/
│   │   ├── App.vue     # Interfața principală
│   │   └── main.js
│   ├── package.json
│   └── vite.config.js
└── README.md
```

## ⚙️ Cum să rulezi local (Dezvoltare)

**1. Baza de Date**
Asigură-te că ai PostgreSQL instalat și configurează un utilizator și o bază de date (ex. `onion_spider`). Actualizează DSN-ul în `backend/cmd/api/main.go`.

**2. Pornire API (Go)**
```bash
cd backend
go run ./cmd/api/main.go
# Serverul va porni pe portul 8888
```

**3. Pornire Frontend (Vue)**
```bash
cd frontend
npm install
npm run dev
# Dashboard-ul va fi disponibil pe portul 5173
```

## 🌐 Deployment (Nginx)

Proiectul este configurat să ruleze în producție cu Nginx acționând ca reverse proxy:
- Servește fișierele statice din `frontend/dist/` pe portul 80/443.
- Redirecționează request-urile `/api/*` către binarul Go care rulează pe portul `8888`.
