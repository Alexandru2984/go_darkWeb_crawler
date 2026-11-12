package api

import (
	"errors"
	"net/http"

	"onion-spider/internal/database"
)

func (d *deps) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if !d.searchLim.Allow(RequestKey(r)) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many result requests. Try again in a minute.")
		return
	}
	var input database.BrowseInput
	if !decodeJSONBody(w, r, 8192, &input) {
		return
	}
	limit, offset := ParsePagination(r)
	nodes, err := d.cfg.DB.BrowseNodes(r.Context(), input, limit+1, offset, GetUserID(r), HasAdminAccess(r))
	if err != nil {
		savedSearchError(w, r, err)
		return
	}
	if nodes == nil {
		nodes = []database.Node{}
	}
	writeNodePage(w, nodes, limit)
}

func (d *deps) allowCollectionWrite(w http.ResponseWriter, r *http.Request) bool {
	if !d.collectionLim.Allow(RequestKey(r)) {
		WriteJSONError(w, http.StatusTooManyRequests, "Too many collection changes. Try again in a minute.")
		return false
	}
	return true
}

func collectionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, database.ErrCollectionMissing):
		WriteJSONError(w, http.StatusNotFound, "Collection not found")
	case errors.Is(err, database.ErrCollectionDuplicate), errors.Is(err, database.ErrCollectionLimit):
		WriteJSONError(w, http.StatusConflict, err.Error())
	default:
		annotationError(w, r, "collection", err)
	}
}

func (d *deps) handleCollectionAdd(w http.ResponseWriter, r *http.Request) {
	if !d.allowCollectionWrite(w, r) {
		return
	}
	var input struct {
		URL string `json:"url"`
		Tag string `json:"tag"`
	}
	if !decodeJSONBody(w, r, 4096, &input) {
		return
	}
	nodeURL := resolveAnnotationTarget(w, input.URL)
	if nodeURL == "" {
		return
	}
	tag, err := database.NormalizeCollectionName(input.Tag)
	if err != nil {
		collectionError(w, r, err)
		return
	}
	if err := d.cfg.DB.AddTagContext(r.Context(), nodeURL, GetUserID(r), tag); err != nil {
		collectionError(w, r, err)
		return
	}
	writeNoStoreJSON(w, http.StatusOK, map[string]string{"tag": tag, "message": "Site added to your collection."})
}

func (d *deps) handleCollectionRename(w http.ResponseWriter, r *http.Request) {
	if !d.allowCollectionWrite(w, r) {
		return
	}
	var input struct {
		Tag  string `json:"tag"`
		Name string `json:"name"`
	}
	if !decodeJSONBody(w, r, 2048, &input) {
		return
	}
	count, err := d.cfg.DB.RenameCollection(r.Context(), GetUserID(r), input.Tag, input.Name)
	if err != nil {
		collectionError(w, r, err)
		return
	}
	tag, _ := database.NormalizeCollectionName(input.Name)
	writeNoStoreJSON(w, http.StatusOK, map[string]any{"tag": tag, "sites": count, "message": "Collection renamed. Saved searches follow the new name."})
}

func (d *deps) handleCollectionDelete(w http.ResponseWriter, r *http.Request) {
	if !d.allowCollectionWrite(w, r) {
		return
	}
	var input struct {
		Tag string `json:"tag"`
	}
	if !decodeJSONBody(w, r, 1024, &input) {
		return
	}
	count, err := d.cfg.DB.DeleteCollection(r.Context(), GetUserID(r), input.Tag)
	if err != nil {
		collectionError(w, r, err)
		return
	}
	writeNoStoreJSON(w, http.StatusOK, map[string]any{"sites": count, "message": "Collection removed. Crawl records, notes and watches are kept."})
}
