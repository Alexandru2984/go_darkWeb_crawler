package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (d *deps) handleBlacklistList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	domains, err := d.cfg.DB.GetBlacklist()
	if err != nil {
		slog.ErrorContext(r.Context(), "blacklist_list_failed", "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if domains == nil {
		domains = []string{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"domains": domains})
}

func (d *deps) handleBlacklistAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Domain string `json:"domain"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 512)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		WriteJSONError(w, http.StatusBadRequest, "Invalid body")
		return
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if req.Domain == "" {
		WriteJSONError(w, http.StatusBadRequest, "The 'domain' field is required")
		return
	}
	if !strings.HasSuffix(req.Domain, ".onion") {
		WriteJSONError(w, http.StatusBadRequest, "Only .onion domains can be blocked")
		return
	}
	if err := d.cfg.DB.AddBlacklist(req.Domain); err != nil {
		slog.ErrorContext(ctx, "blacklist_add_failed", "domain", req.Domain, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	slog.InfoContext(ctx, "blacklist_add", "admin_user", GetUserID(r), "domain", req.Domain)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Domain blocked: %s", req.Domain)})
}

func (d *deps) handleBlacklistDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	domain := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "domain")))
	if domain == "" || !strings.HasSuffix(domain, ".onion") {
		WriteJSONError(w, http.StatusBadRequest, "Invalid domain: must be a .onion domain")
		return
	}
	found, err := d.cfg.DB.DeleteBlacklist(domain)
	if err != nil {
		slog.ErrorContext(ctx, "blacklist_delete_failed", "domain", domain, "err", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if !found {
		WriteJSONError(w, http.StatusNotFound, "Domain not found in blacklist")
		return
	}
	slog.InfoContext(ctx, "blacklist_remove", "admin_user", GetUserID(r), "domain", domain)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Domain removed from blacklist: %s", domain)})
}
