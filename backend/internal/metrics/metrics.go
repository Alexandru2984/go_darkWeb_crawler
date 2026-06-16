// Package metrics defines the Prometheus collectors for Onion Spider and
// registers them with the default registry (exposed at /metrics by main).
//
// The /metrics, /healthz and /readyz endpoints are served on the root mux,
// NOT under /api — so the production nginx (which only proxies /api/ and serves
// static files for everything else) never exposes them publicly. A local
// Prometheus scrapes http://127.0.0.1:8900/metrics directly.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTPRequests counts API requests by method, matched chi route pattern and
	// status. Rate-limit rejections are visible here as status="429".
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "onionspider_http_requests_total",
		Help: "Total HTTP requests by method, route and status code.",
	}, []string{"method", "route", "status"})

	// HTTPDuration tracks request latency per route.
	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "onionspider_http_request_duration_seconds",
		Help:    "HTTP request latency in seconds by route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route"})

	// CrawlsTotal counts crawl attempts by outcome.
	// result ∈ {success, scrape_error, robots_blocked, non_onion}.
	CrawlsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "onionspider_crawls_total",
		Help: "Crawl attempts by result.",
	}, []string{"result"})

	// LinksDiscovered counts .onion links extracted across all crawls.
	LinksDiscovered = promauto.NewCounter(prometheus.CounterOpts{
		Name: "onionspider_links_discovered_total",
		Help: "Total .onion links discovered across crawls.",
	})

	// QueueNodes reports the number of nodes in each processing_status. It is
	// refreshed periodically by a background poller in main.
	QueueNodes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "onionspider_queue_nodes",
		Help: "Number of nodes by processing_status (refreshed periodically).",
	}, []string{"status"})
)
