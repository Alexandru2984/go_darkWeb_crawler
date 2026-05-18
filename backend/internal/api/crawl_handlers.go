package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"onion-spider/internal/database"
)

func (d *deps) handleCrawl(w http.ResponseWriter, r *http.Request) {
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
	log.Printf("[AUDIT] POST /api/crawl ip=%q user=%d url=%q", ip, GetUserID(r), req.URL)
	if err := d.cfg.Engine.AddToQueue(req.URL, GetUserID(r)); err != nil {
		if errors.Is(err, database.ErrBlacklisted) {
			WriteJSONError(w, http.StatusForbidden, "Domain blocked")
			return
		}
		log.Printf("[ERROR] POST /api/crawl user=%d url=%q: %v", GetUserID(r), req.URL, err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"message": "URL added to the crawling queue"})
}

func (d *deps) handleRecrawl(w http.ResponseWriter, r *http.Request) {
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
		log.Printf("[ERROR] POST /api/recrawl url=%q: %v", req.URL, err)
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
	log.Printf("[AUDIT] POST /api/recrawl ip=%q url=%q", ip, req.URL)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"message": "Node has been queued for re-crawling"})
}

func (d *deps) handleCrawlBulk(w http.ResponseWriter, r *http.Request) {
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
		log.Printf("[AUDIT] POST /api/crawl/bulk ip=%q user=%d url=%q", ip, GetUserID(r), u)
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
		log.Printf("[ERROR] GET /api/queue: %v", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	json.NewEncoder(w).Encode(summary)
}
