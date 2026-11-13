package api

import (
	"errors"
	"log/slog"
	"net/http"
	"onion-spider/internal/database"
	"time"
)

func crawlControlError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, database.ErrQueueQuotaExceeded):
		WriteJSONError(w, 429, "Your pending queue has reached its submission limit.")
	case errors.Is(err, database.ErrBlacklisted):
		WriteJSONError(w, 403, "Domain blocked")
	case errors.Is(err, database.ErrCrawlBusy):
		WriteJSONError(w, 409, err.Error())
	case errors.Is(err, database.ErrCrawlSchedule), errors.Is(err, database.ErrCrawlBatch), errors.Is(err, database.ErrSearchCriteria):
		WriteJSONError(w, 400, err.Error())
	default:
		annotationError(w, r, "crawl_control", err)
	}
}
func (d *deps) allowCrawlControl(w http.ResponseWriter, r *http.Request) bool {
	if !d.queueLim.Allow(RequestKey(r)) {
		WriteJSONError(w, 429, "Too many queue changes. Try again in a minute.")
		return false
	}
	return true
}
func (d *deps) handleQueuePause(w http.ResponseWriter, r *http.Request) {
	if !d.allowCrawlControl(w, r) {
		return
	}
	var input struct {
		Paused *bool `json:"paused"`
	}
	if !decodeJSONBody(w, r, 256, &input) {
		return
	}
	if input.Paused == nil {
		WriteJSONError(w, 400, "paused must be true or false")
		return
	}
	if err := d.cfg.DB.SetCrawlPaused(r.Context(), GetUserID(r), *input.Paused); err != nil {
		crawlControlError(w, r, err)
		return
	}
	slog.InfoContext(r.Context(), "crawl_pause_changed", "uid", GetUserID(r), "paused", *input.Paused)
	writeNoStoreJSON(w, 200, map[string]any{"paused": *input.Paused})
}
func (d *deps) handleQueueJobs(w http.ResponseWriter, r *http.Request) {
	if !d.searchLim.Allow(RequestKey(r)) {
		WriteJSONError(w, 429, "Too many queue requests. Try again in a minute.")
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if !decodeJSONBody(w, r, 256, &input) {
		return
	}
	limit, offset := ParsePagination(r)
	jobs, err := d.cfg.DB.ListCrawlJobs(r.Context(), GetUserID(r), input.Status, limit+1, offset)
	if err != nil {
		crawlControlError(w, r, err)
		return
	}
	more := len(jobs) > limit
	if more {
		jobs = jobs[:limit]
	}
	writeNoStoreJSON(w, 200, map[string]any{"jobs": jobs, "has_more": more})
}
func (d *deps) handleQueueAction(w http.ResponseWriter, r *http.Request) {
	if !d.allowCrawlControl(w, r) {
		return
	}
	var input struct {
		URL         string `json:"url"`
		Action      string `json:"action"`
		ScheduledAt string `json:"scheduled_at"`
	}
	if !decodeJSONBody(w, r, 16384, &input) {
		return
	}
	url := resolveAnnotationTarget(w, input.URL)
	if url == "" {
		return
	}
	if input.Action != "schedule" && input.ScheduledAt != "" {
		WriteJSONError(w, 400, "scheduled_at is only allowed for scheduling")
		return
	}
	var err error
	switch input.Action {
	case "cancel":
		err = d.cfg.DB.CancelCrawl(r.Context(), GetUserID(r), url)
	case "retry", "schedule":
		var when time.Time
		if input.Action == "schedule" {
			when, err = time.Parse(time.RFC3339, input.ScheduledAt)
			if err != nil {
				WriteJSONError(w, 400, "Use an RFC3339 scheduled_at value including its timezone")
				return
			}
		}
		var found, allowed bool
		found, allowed, err = d.cfg.DB.ScheduleCrawl(r.Context(), GetUserID(r), url, when, d.cfg.Engine.MaxPendingPerUser)
		if err == nil && !found {
			err = database.ErrNodeNotFound
		}
		if err == nil && !allowed {
			err = database.ErrCrawlBusy
		}
	default:
		WriteJSONError(w, 400, "Choose cancel, retry or schedule")
		return
	}
	if err != nil {
		crawlControlError(w, r, err)
		return
	}
	slog.InfoContext(r.Context(), "crawl_job_changed", "uid", GetUserID(r), "action", input.Action)
	writeNoStoreJSON(w, 200, map[string]string{"message": "Crawl schedule updated."})
}
func (d *deps) handleCrawlPreview(w http.ResponseWriter, r *http.Request) {
	if !d.searchLim.Allow(RequestKey(r)) {
		WriteJSONError(w, 429, "Too many preview requests. Try again in a minute.")
		return
	}
	var input struct {
		URLs []string `json:"urls"`
	}
	if !decodeJSONBody(w, r, 256*1024, &input) {
		return
	}
	result, err := d.cfg.DB.CrawlBatch(r.Context(), GetUserID(r), input.URLs, d.cfg.Engine.MaxPendingPerUser, true)
	if err != nil {
		crawlControlError(w, r, err)
		return
	}
	writeNoStoreJSON(w, 200, result)
}
