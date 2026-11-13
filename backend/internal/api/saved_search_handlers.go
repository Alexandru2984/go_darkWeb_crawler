package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"onion-spider/internal/database"
)

func savedSearchError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, database.ErrSearchCriteria), errors.Is(err, database.ErrSavedSearchName), errors.Is(err, database.ErrResearchFilters), errors.Is(err, database.ErrCollectionName):
		WriteJSONError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, database.ErrSavedSearchNotFound):
		WriteJSONError(w, http.StatusNotFound, "Saved search not found")
	case errors.Is(err, database.ErrSavedSearchDuplicate), errors.Is(err, database.ErrSavedSearchLimit):
		WriteJSONError(w, http.StatusConflict, err.Error())
	default:
		slog.ErrorContext(r.Context(), "saved_search_failed", "uid", GetUserID(r), "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Could not update saved searches")
	}
}

func (d *deps) handleSavedSearchList(w http.ResponseWriter, r *http.Request) {
	searches, err := d.cfg.DB.ListSavedSearches(r.Context(), GetUserID(r))
	if err != nil {
		savedSearchError(w, r, err)
		return
	}
	writeNoStoreJSON(w, http.StatusOK, map[string]any{"searches": searches, "limit": database.MaxSavedSearches})
}

func (d *deps) allowSavedSearchWrite(w http.ResponseWriter, r *http.Request) bool {
	if !d.savedSearchLim.Allow(RequestKey(r)) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many saved search changes. Try again in a minute.")
		return false
	}
	return true
}

func (d *deps) handleSavedSearchCreate(w http.ResponseWriter, r *http.Request) {
	if !d.allowSavedSearchWrite(w, r) {
		return
	}
	var input database.SavedSearchInput
	if !decodeJSONBody(w, r, 16384, &input) {
		return
	}
	saved, err := d.cfg.DB.CreateSavedSearch(r.Context(), GetUserID(r), input)
	if err != nil {
		savedSearchError(w, r, err)
		return
	}
	writeNoStoreJSON(w, http.StatusCreated, saved)
}

func savedSearchID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		WriteJSONError(w, http.StatusBadRequest, "Invalid saved search")
		return 0, false
	}
	return id, true
}

func (d *deps) handleSavedSearchRename(w http.ResponseWriter, r *http.Request) {
	if !d.allowSavedSearchWrite(w, r) {
		return
	}
	id, ok := savedSearchID(w, r)
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSONBody(w, r, 1024, &input) {
		return
	}
	saved, err := d.cfg.DB.RenameSavedSearch(r.Context(), GetUserID(r), id, input.Name)
	if err != nil {
		savedSearchError(w, r, err)
		return
	}
	writeNoStoreJSON(w, http.StatusOK, saved)
}

func (d *deps) handleSavedSearchDelete(w http.ResponseWriter, r *http.Request) {
	if !d.allowSavedSearchWrite(w, r) {
		return
	}
	id, ok := savedSearchID(w, r)
	if !ok {
		return
	}
	if err := d.cfg.DB.DeleteSavedSearch(r.Context(), GetUserID(r), id); err != nil {
		savedSearchError(w, r, err)
		return
	}
	writeNoStoreJSON(w, http.StatusOK, map[string]string{"message": "Saved search deleted."})
}
