package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (d *deps) handleBlacklistList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	domains, err := d.cfg.DB.GetBlacklist()
	if err != nil {
		log.Printf("[ERROR] GET /api/blacklist: %v", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if domains == nil {
		domains = []string{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"domains": domains})
}

func (d *deps) handleBlacklistAdd(w http.ResponseWriter, r *http.Request) {
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
		log.Printf("[ERROR] POST /api/blacklist domain=%s: %v", SanitizeForLog(req.Domain), err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	log.Printf("[AUDIT] blacklist_add admin_user=%d domain=%s", GetUserID(r), SanitizeForLog(req.Domain))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Domain blocked: %s", req.Domain)})
}

func (d *deps) handleBlacklistDelete(w http.ResponseWriter, r *http.Request) {
	domain := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "domain")))
	if domain == "" || !strings.HasSuffix(domain, ".onion") {
		WriteJSONError(w, http.StatusBadRequest, "Invalid domain: must be a .onion domain")
		return
	}
	found, err := d.cfg.DB.DeleteBlacklist(domain)
	if err != nil {
		log.Printf("[ERROR] DELETE /api/blacklist domain=%s: %v", SanitizeForLog(domain), err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if !found {
		WriteJSONError(w, http.StatusNotFound, "Domain not found in blacklist")
		return
	}
	log.Printf("[AUDIT] blacklist_remove admin_user=%d domain=%s", GetUserID(r), SanitizeForLog(domain))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": fmt.Sprintf("Domain removed from blacklist: %s", domain)})
}
