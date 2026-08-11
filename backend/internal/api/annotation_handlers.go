package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"onion-spider/internal/database"
)

// Every endpoint in this file takes the .onion address in a POST body, never in
// a path segment or query string — including the ones that only read.
//
// A URL in a request line ends up in far more places than the request: browser
// history, the Referer header on the next navigation, proxy and CDN access logs,
// and any screenshot of the address bar. For a service whose entire purpose is
// that nobody learns which hidden services an account is interested in, that is
// the wrong shape regardless of how convenient GET would be.

// resolveAnnotationTarget reads and validates the URL every handler here needs.
// Returns "" after writing the response when the caller should stop.
func resolveAnnotationTarget(w http.ResponseWriter, raw string) string {
	nodeURL := NormalizeOnionURL(raw)
	if nodeURL == "" {
		WriteJSONError(w, http.StatusBadRequest, "A valid onion URL is required")
		return ""
	}
	return nodeURL
}

// annotationError maps the storage layer's errors onto responses.
//
// A site belonging to another account and a site that does not exist both
// answer 404. Distinguishing them would turn this into an oracle for which
// .onion addresses somebody else is tracking.
func annotationError(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, database.ErrNodeNotFound):
		WriteJSONError(w, http.StatusNotFound, "That site is not in your graph. Crawl it first.")
	case errors.Is(err, database.ErrTooManyTags):
		WriteJSONError(w, http.StatusBadRequest, "That site already has the maximum number of tags.")
	case errors.Is(err, database.ErrInvalidOnionURL):
		WriteJSONError(w, http.StatusBadRequest, "A valid onion URL is required")
	default:
		slog.ErrorContext(r.Context(), op+"_failed", "uid", GetUserID(r), "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
	}
}

// handleAnnotation returns the account's tags, note and watch for one site.
func (d *deps) handleAnnotation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if !decodeJSONBody(w, r, 2300, &req) {
		return
	}
	nodeURL := resolveAnnotationTarget(w, req.URL)
	if nodeURL == "" {
		return
	}
	annotation, err := d.cfg.DB.GetAnnotation(nodeURL, GetUserID(r))
	if err != nil {
		annotationError(w, r, "get_annotation", err)
		return
	}
	writeNoStoreJSON(w, http.StatusOK, annotation)
}

// handleTag adds or removes one label.
func (d *deps) handleTag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string `json:"url"`
		Tag    string `json:"tag"`
		Remove bool   `json:"remove,omitempty"`
	}
	if !decodeJSONBody(w, r, 2500, &req) {
		return
	}
	nodeURL := resolveAnnotationTarget(w, req.URL)
	if nodeURL == "" {
		return
	}
	if tag := database.NormalizeTag(req.Tag); tag == "" || len(tag) > database.MaxTagLen {
		WriteJSONError(w, http.StatusBadRequest,
			"A tag must be between 1 and "+strconv.Itoa(database.MaxTagLen)+" characters")
		return
	}

	uid := GetUserID(r)
	var err error
	if req.Remove {
		err = d.cfg.DB.RemoveTag(nodeURL, uid, req.Tag)
	} else {
		err = d.cfg.DB.AddTag(nodeURL, uid, req.Tag)
	}
	if err != nil {
		annotationError(w, r, "tag", err)
		return
	}
	annotation, err := d.cfg.DB.GetAnnotation(nodeURL, uid)
	if err != nil {
		annotationError(w, r, "get_annotation", err)
		return
	}
	writeNoStoreJSON(w, http.StatusOK, annotation)
}

// handleNote replaces the account's note on a site. An empty body clears it.
func (d *deps) handleNote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  string `json:"url"`
		Body string `json:"body"`
	}
	// Sized for the note limit plus the URL and JSON framing.
	if !decodeJSONBody(w, r, database.MaxNoteLen+3000, &req) {
		return
	}
	nodeURL := resolveAnnotationTarget(w, req.URL)
	if nodeURL == "" {
		return
	}
	if len(req.Body) > database.MaxNoteLen {
		WriteJSONError(w, http.StatusBadRequest,
			"A note can be at most "+strconv.Itoa(database.MaxNoteLen)+" characters")
		return
	}
	uid := GetUserID(r)
	if err := d.cfg.DB.SetNote(nodeURL, uid, req.Body); err != nil {
		annotationError(w, r, "set_note", err)
		return
	}
	annotation, err := d.cfg.DB.GetAnnotation(nodeURL, uid)
	if err != nil {
		annotationError(w, r, "get_annotation", err)
		return
	}
	writeNoStoreJSON(w, http.StatusOK, annotation)
}

// handleTags lists the account's labels with usage counts.
func (d *deps) handleTags(w http.ResponseWriter, r *http.Request) {
	tags, err := d.cfg.DB.ListTags(GetUserID(r))
	if err != nil {
		annotationError(w, r, "list_tags", err)
		return
	}
	writeNoStoreJSON(w, http.StatusOK, tags)
}

// handleWatch starts, updates or stops watching a site.
func (d *deps) handleWatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL          string `json:"url"`
		IntervalDays int    `json:"interval_days,omitempty"`
		Stop         bool   `json:"stop,omitempty"`
	}
	if !decodeJSONBody(w, r, 2500, &req) {
		return
	}
	nodeURL := resolveAnnotationTarget(w, req.URL)
	if nodeURL == "" {
		return
	}
	uid := GetUserID(r)

	if req.Stop {
		if err := d.cfg.DB.StopWatch(nodeURL, uid); err != nil {
			annotationError(w, r, "stop_watch", err)
			return
		}
		slog.InfoContext(r.Context(), "watch_stopped", "uid", uid)
		writeNoStoreJSON(w, http.StatusOK, map[string]any{"watch": nil, "message": "No longer watching."})
		return
	}

	if req.IntervalDays == 0 {
		req.IntervalDays = database.MinWatchIntervalDays
	}
	if req.IntervalDays < database.MinWatchIntervalDays || req.IntervalDays > database.MaxWatchIntervalDays {
		WriteJSONError(w, http.StatusBadRequest,
			"Check between every "+strconv.Itoa(database.MinWatchIntervalDays)+
				" and "+strconv.Itoa(database.MaxWatchIntervalDays)+" days")
		return
	}
	watch, err := d.cfg.DB.StartWatch(nodeURL, uid, req.IntervalDays)
	if err != nil {
		annotationError(w, r, "start_watch", err)
		return
	}
	slog.InfoContext(r.Context(), "watch_started", "uid", uid, "interval_days", req.IntervalDays)
	writeNoStoreJSON(w, http.StatusOK, map[string]any{
		"watch":   watch,
		"message": "Watching. You will see changes in the feed after the next crawl.",
	})
}

// handleWatches lists everything the account is watching.
func (d *deps) handleWatches(w http.ResponseWriter, r *http.Request) {
	watches, err := d.cfg.DB.ListWatches(GetUserID(r))
	if err != nil {
		annotationError(w, r, "list_watches", err)
		return
	}
	writeNoStoreJSON(w, http.StatusOK, watches)
}

// handleWatchEvents returns the change feed and the unread count.
//
// The feed is read here rather than sent by email on purpose: a message saying
// "the site you are watching changed" tells whoever handles that mailbox — and
// every server between here and it — what this account is interested in.
func (d *deps) handleWatchEvents(w http.ResponseWriter, r *http.Request) {
	uid := GetUserID(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := d.cfg.DB.ListWatchEvents(uid, limit)
	if err != nil {
		annotationError(w, r, "list_watch_events", err)
		return
	}
	unseen, err := d.cfg.DB.CountUnseenWatchEvents(uid)
	if err != nil {
		annotationError(w, r, "count_watch_events", err)
		return
	}
	writeNoStoreJSON(w, http.StatusOK, map[string]any{"events": events, "unseen": unseen})
}

// handleWatchEventsSeen clears the unread badge.
func (d *deps) handleWatchEventsSeen(w http.ResponseWriter, r *http.Request) {
	n, err := d.cfg.DB.MarkWatchEventsSeen(GetUserID(r))
	if err != nil {
		annotationError(w, r, "mark_watch_events_seen", err)
		return
	}
	writeNoStoreJSON(w, http.StatusOK, map[string]any{"marked": n})
}
