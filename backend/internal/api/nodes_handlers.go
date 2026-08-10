package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

func (d *deps) handleNodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	limit, offset := ParsePagination(r)
	nodes, err := d.cfg.DB.GetNodes(limit, offset, GetUserID(r), IsAdmin(r))
	if err != nil {
		slog.ErrorContext(r.Context(), "get_nodes_failed", "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	json.NewEncoder(w).Encode(nodes)
}

func (d *deps) handleNode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req struct {
		URL string `json:"url"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2300)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Invalid body")
		return
	}
	nodeURL := NormalizeOnionURL(req.URL)
	if nodeURL == "" {
		WriteJSONError(w, http.StatusBadRequest, "A valid onion URL is required")
		return
	}
	node, err := d.cfg.DB.GetNodeByURL(nodeURL, GetUserID(r), IsAdmin(r))
	if err != nil {
		slog.ErrorContext(r.Context(), "get_node_failed", "url", nodeURL, "err", err)
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
		slog.ErrorContext(r.Context(), "get_edges_failed", "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	json.NewEncoder(w).Encode(edges)
}

func (d *deps) handleSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !d.searchLim.Allow(RequestKey(r)) {
		WriteJSONError(w, http.StatusTooManyRequests, "Rate limit exceeded — max 60 searches/minute")
		return
	}
	var req struct {
		Query    string `json:"q"`
		Category string `json:"category,omitempty"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Invalid body")
		return
	}
	q := strings.TrimSpace(req.Query)
	if q == "" {
		WriteJSONError(w, http.StatusBadRequest, "Search query is required")
		return
	}
	if len(q) > 200 {
		WriteJSONError(w, http.StatusBadRequest, "Query too long (max 200 characters)")
		return
	}
	category := req.Category
	if len(category) > 50 {
		WriteJSONError(w, http.StatusBadRequest, "Category too long")
		return
	}
	nodes, err := d.cfg.DB.SearchNodes(q, category, GetUserID(r), IsAdmin(r))
	if err != nil {
		slog.ErrorContext(r.Context(), "search_failed", "q", q, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	json.NewEncoder(w).Encode(nodes)
}
