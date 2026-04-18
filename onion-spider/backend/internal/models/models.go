package models

import "time"

// Node reprezinta un site .onion in reteaua noastra
type Node struct {
	ID            int       `json:"id"`
	URL           string    `json:"url"`
	Title         string    `json:"title,omitempty"`
	StatusCode    int       `json:"status_code,omitempty"`
	ServerHeader  string    `json:"server_header,omitempty"`
	Metadata      []byte    `json:"metadata,omitempty"` // mapat la JSONB in bd
	DiscoveredAt  time.Time `json:"discovered_at"`
	LastCrawledAt time.Time `json:"last_crawled_at,omitempty"`
}

// Edge reprezinta legatura intre doua site-uri
type Edge struct {
	SourceURL    string    `json:"source_url"`
	TargetURL    string    `json:"target_url"`
	DiscoveredAt time.Time `json:"discovered_at"`
}
