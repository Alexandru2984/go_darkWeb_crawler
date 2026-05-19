package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"onion-spider/internal/database"
)

func (d *deps) handleCrawl(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := ClientIP(r)
	if !IsAdmin(r) && !d.crawlLim.Allow(ip) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many requests. Please try again in a few minutes.")
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Invalid body")
		return
	}
	req.URL = NormalizeOnionURL(req.URL)
	if !IsValidOnionURL(req.URL) {
		WriteJSONError(w, http.StatusBadRequest, "Invalid URL: must be a valid .onion v3 URL (http/https)")
		return
	}
	slog.InfoContext(ctx, "crawl_request", "ip", ip, "user", GetUserID(r), "url", req.URL)
	if err := d.cfg.Engine.AddToQueue(req.URL, GetUserID(r)); err != nil {
		if errors.Is(err, database.ErrBlacklisted) {
			WriteJSONError(w, http.StatusForbidden, "Domain blocked")
			return
		}
		slog.ErrorContext(ctx, "crawl_enqueue_failed", "user", GetUserID(r), "url", req.URL, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"message": "URL added to the crawling queue"})
}

func (d *deps) handleRecrawl(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := ClientIP(r)
	if !IsAdmin(r) && !d.crawlLim.Allow(ip) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many requests. Please try again in a few minutes.")
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Invalid body")
		return
	}
	req.URL = NormalizeOnionURL(req.URL)
	if !IsValidOnionURL(req.URL) {
		WriteJSONError(w, http.StatusBadRequest, "Invalid URL")
		return
	}
	found, canRequeue, err := d.cfg.DB.RequeueForCrawl(req.URL, GetUserID(r))
	if err != nil {
		slog.ErrorContext(ctx, "recrawl_failed", "url", req.URL, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if !found {
		WriteJSONError(w, http.StatusNotFound, "URL does not exist in the database")
		return
	}
	if !canRequeue {
		WriteJSONError(w, http.StatusConflict, "Node is already being crawled")
		return
	}
	slog.InfoContext(ctx, "recrawl_request", "ip", ip, "url", req.URL)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"message": "Node has been queued for re-crawling"})
}

func (d *deps) handleCrawlBulk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := ClientIP(r)
	if !IsAdmin(r) && !d.crawlLim.Allow(ip) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many requests. Please try again in a few minutes.")
		return
	}
	var req struct {
		URLs []string `json:"urls"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Invalid body")
		return
	}
	if len(req.URLs) == 0 || len(req.URLs) > 20 {
		WriteJSONError(w, http.StatusBadRequest, "Send 1-20 URLs in the 'urls' field")
		return
	}
	var added, skipped int
	for _, u := range req.URLs {
		u = NormalizeOnionURL(u)
		if !IsValidOnionURL(u) {
			skipped++
			continue
		}
		slog.InfoContext(ctx, "crawl_bulk_item", "ip", ip, "user", GetUserID(r), "url", u)
		if err := d.cfg.Engine.AddToQueue(u, GetUserID(r)); err != nil {
			skipped++
		} else {
			added++
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]int{"added": added, "skipped": skipped})
}

func (d *deps) handleQueue(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	summary, err := d.cfg.DB.GetQueueSummary(GetUserID(r), IsAdmin(r))
	if err != nil {
		slog.ErrorContext(r.Context(), "get_queue_failed", "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	json.NewEncoder(w).Encode(summary)
}
