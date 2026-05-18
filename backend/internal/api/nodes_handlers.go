package api

import (
	"encoding/json"
	"log"
	"net/http"
)

func (d *deps) handleNodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	limit, offset := ParsePagination(r)
	nodes, err := d.cfg.DB.GetNodes(limit, offset, GetUserID(r), IsAdmin(r))
	if err != nil {
		log.Printf("[ERROR] GET /api/nodes: %v", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	json.NewEncoder(w).Encode(nodes)
}

func (d *deps) handleNode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	nodeURL := r.URL.Query().Get("url")
	if nodeURL == "" {
		WriteJSONError(w, http.StatusBadRequest, "Parameter 'url' is required")
		return
	}
	if len(nodeURL) > 2048 {
		WriteJSONError(w, http.StatusBadRequest, "Parameter 'url' exceeds maximum length")
		return
	}
	node, err := d.cfg.DB.GetNodeByURL(nodeURL, GetUserID(r), IsAdmin(r))
	if err != nil {
		log.Printf("[ERROR] GET /api/node url=%s: %v", SanitizeForLog(nodeURL), err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if node == nil {
		WriteJSONError(w, http.StatusNotFound, "Node not found")
		return
	}
	json.NewEncoder(w).Encode(node)
}

func (d *deps) handleEdges(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	limit, offset := ParsePagination(r)
	edges, err := d.cfg.DB.GetEdges(limit, offset, GetUserID(r), IsAdmin(r))
	if err != nil {
		log.Printf("[ERROR] GET /api/edges: %v", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	json.NewEncoder(w).Encode(edges)
}

func (d *deps) handleSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ip := ClientIP(r)
	if !d.searchLim.Allow(ip) {
		WriteJSONError(w, http.StatusTooManyRequests, "Rate limit exceeded — max 60 searches/minute")
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		WriteJSONError(w, http.StatusBadRequest, "Parameter 'q' is required")
		return
	}
	if len(q) > 200 {
		WriteJSONError(w, http.StatusBadRequest, "Query too long (max 200 characters)")
		return
	}
	category := r.URL.Query().Get("category")
	nodes, err := d.cfg.DB.SearchNodes(q, category, GetUserID(r), IsAdmin(r))
	if err != nil {
		log.Printf("[ERROR] GET /api/search q=%s: %v", SanitizeForLog(q), err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	json.NewEncoder(w).Encode(nodes)
}
