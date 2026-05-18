package api

import (
	"encoding/json"
	"log"
	"net/http"

	"onion-spider/internal/database"
)

func (d *deps) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var resp struct {
		Status        string `json:"status"`
		DBConnected   bool   `json:"db_connected"`
		NodesCrawled  int    `json:"nodes_crawled"`
		PendingNodes  int    `json:"pending_nodes"`
		FailedNodes   int    `json:"failed_nodes"`
		CrawlingNodes int    `json:"crawling_nodes"`
		BlockedNodes  int    `json:"blocked_nodes"`
		TotalEdges    int    `json:"total_edges"`
		ActiveWorkers int    `json:"active_workers"`
	}
	resp.Status = "online"
	resp.ActiveWorkers = d.cfg.Workers
	stats, err := d.cfg.DB.GetStats(GetUserID(r), IsAdmin(r))
	if err == nil {
		resp.DBConnected = true
		resp.NodesCrawled = stats.NodesCrawled
		resp.PendingNodes = stats.PendingNodes
		resp.FailedNodes = stats.FailedNodes
		resp.CrawlingNodes = stats.CrawlingNodes
		resp.BlockedNodes = stats.BlockedNodes
		resp.TotalEdges = stats.TotalEdges
	}
	json.NewEncoder(w).Encode(resp)
}

func (d *deps) handleTimeline(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats, err := d.cfg.DB.GetTimelineStats(GetUserID(r), IsAdmin(r))
	if err != nil {
		log.Printf("[ERROR] GET /api/stats/timeline: %v", err)
		WriteJSONError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	if stats == nil {
		stats = []database.TimelineStat{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"timeline": stats})
}
