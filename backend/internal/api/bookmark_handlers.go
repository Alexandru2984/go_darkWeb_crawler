package api

import (
	"errors"
	"net/http"
	"onion-spider/internal/database"
)

func (d *deps) handleBookmark(w http.ResponseWriter, r *http.Request) {
	if !d.bookmarkLim.Allow(RequestKey(r)) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many bookmark changes. Try again in a minute.")
		return
	}
	var input struct {
		URL    string `json:"url"`
		Remove bool   `json:"remove"`
		Review string `json:"review"`
	}
	if !decodeJSONBody(w, r, 4096, &input) {
		return
	}
	url := resolveAnnotationTarget(w, input.URL)
	if url == "" {
		return
	}
	if input.Remove && input.Review != "" {
		WriteJSONError(w, http.StatusBadRequest, "Choose remove or review, not both")
		return
	}
	var err error
	if input.Review != "" {
		err = d.cfg.DB.SetBookmarkReview(r.Context(), GetUserID(r), url, input.Review)
	} else {
		err = d.cfg.DB.SetBookmark(r.Context(), GetUserID(r), url, input.Remove)
	}
	if err != nil {
		switch {
		case errors.Is(err, database.ErrBookmarkMissing):
			WriteJSONError(w, 404, err.Error())
		case errors.Is(err, database.ErrBookmarkLimit):
			WriteJSONError(w, 409, err.Error())
		case errors.Is(err, database.ErrReviewState):
			WriteJSONError(w, 400, err.Error())
		default:
			annotationError(w, r, "bookmark", err)
		}
		return
	}
	annotation, err := d.cfg.DB.GetAnnotation(url, GetUserID(r))
	if err != nil {
		annotationError(w, r, "bookmark", err)
		return
	}
	writeNoStoreJSON(w, 200, annotation)
}
func (d *deps) handleWatchQuery(w http.ResponseWriter, r *http.Request) {
	if !d.searchLim.Allow(RequestKey(r)) {
		WriteJSONError(w, 429, "Too many result requests. Try again in a minute.")
		return
	}
	var input database.WatchFeedInput
	if !decodeJSONBody(w, r, 8192, &input) {
		return
	}
	limit, offset := ParsePagination(r)
	events, err := d.cfg.DB.QueryWatchEvents(r.Context(), GetUserID(r), input, limit+1, offset)
	if err != nil {
		savedSearchError(w, r, err)
		return
	}
	more := len(events) > limit
	if more {
		events = events[:limit]
	}
	unseen, err := d.cfg.DB.CountUnseenWatchEvents(GetUserID(r))
	if err != nil {
		annotationError(w, r, "watch_count", err)
		return
	}
	writeNoStoreJSON(w, 200, map[string]any{"events": events, "unseen": unseen, "has_more": more})
}
