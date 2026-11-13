package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"onion-spider/internal/database"
)

func (d *deps) handleCrawl(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := ClientIP(r)
	if !HasAdminAccess(r) && !d.crawlLim.Allow(RequestKey(r)) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many requests. Please try again in a few minutes.")
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if !decodeJSONBody(w, r, 16384, &req) {
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
		if errors.Is(err, database.ErrQueueQuotaExceeded) {
			WriteJSONError(w, http.StatusTooManyRequests, "Crawl queue is full for this account. Wait for pending URLs to be crawled.")
			return
		}
		slog.ErrorContext(ctx, "crawl_enqueue_failed", "user", GetUserID(r), "url", req.URL, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	writeNoStoreJSON(w, http.StatusAccepted, map[string]string{"message": "URL accepted. Existing crawl records are kept; use Queue to retry or reschedule them."})
}

func (d *deps) handleRecrawl(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := ClientIP(r)
	if !HasAdminAccess(r) && !d.crawlLim.Allow(RequestKey(r)) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many requests. Please try again in a few minutes.")
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if !decodeJSONBody(w, r, 16384, &req) {
		return
	}
	req.URL = NormalizeOnionURL(req.URL)
	if !IsValidOnionURL(req.URL) {
		WriteJSONError(w, http.StatusBadRequest, "Invalid URL")
		return
	}
	found, canRequeue, err := d.cfg.DB.ScheduleCrawl(r.Context(), GetUserID(r), req.URL, time.Time{}, d.cfg.Engine.MaxPendingPerUser)
	if err != nil {
		crawlControlError(w, r, err)
		return
	}
	if !found {
		WriteJSONError(w, http.StatusNotFound, "URL does not exist in the database")
		return
	}
	if !canRequeue {
		WriteJSONError(w, http.StatusConflict, "Node is running or blocked; its schedule cannot be changed")
		return
	}
	slog.InfoContext(ctx, "recrawl_request", "ip", ip, "url", req.URL)
	writeNoStoreJSON(w, http.StatusAccepted, map[string]string{"message": "Node has been queued for re-crawling"})
}

func (d *deps) handleCrawlBulk(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URLs []string `json:"urls"`
	}
	if !decodeJSONBody(w, r, 256*1024, &input) {
		return
	}
	if len(input.URLs) < 1 || len(input.URLs) > 20 {
		WriteJSONError(w, 400, database.ErrCrawlBatch.Error())
		return
	}
	if !HasAdminAccess(r) && !d.crawlLim.AllowN(RequestKey(r), len(input.URLs)) {
		WriteJSONError(w, 429, "Too many URLs submitted. Try again in a minute.")
		return
	}
	result, err := d.cfg.DB.CrawlBatch(r.Context(), GetUserID(r), input.URLs, d.cfg.Engine.MaxPendingPerUser, false)
	if err != nil {
		crawlControlError(w, r, err)
		return
	}
	slog.InfoContext(r.Context(), "crawl_batch_accepted", "uid", GetUserID(r), "added", result.Added, "skipped", result.Skipped)
	status := http.StatusAccepted
	if result.QuotaHit {
		status = http.StatusTooManyRequests
	}
	writeNoStoreJSON(w, status, result)
}

func (d *deps) handleQueue(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	summary, err := d.cfg.DB.GetQueueSummary(GetUserID(r), false)
	if err != nil {
		slog.ErrorContext(r.Context(), "get_queue_failed", "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	prefs, err := d.cfg.DB.GetCrawlPreferences(r.Context(), GetUserID(r))
	if err != nil {
		crawlControlError(w, r, err)
		return
	}
	due, err := d.cfg.DB.DueCrawlCount(r.Context(), GetUserID(r))
	if err != nil {
		crawlControlError(w, r, err)
		return
	}
	writeNoStoreJSON(w, 200, struct {
		*database.QueueSummary
		Paused          bool   `json:"paused"`
		SubmissionLimit int    `json:"submission_limit"`
		Due             int    `json:"due"`
		Scope           string `json:"scope"`
	}{summary, prefs.Paused, d.cfg.Engine.MaxPendingPerUser, due, "account"})
}
