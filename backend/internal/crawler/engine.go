package crawler

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"onion-spider/internal/database"
	"onion-spider/internal/proxy"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Engine struct {
	DB               *database.DB
	Proxy            string
	Workers          int
	MaxDepth         int
	TorCtrl          *proxy.TorController // nil if Tor control is not configured
	domainLastAccess map[string]time.Time
	domainMu         sync.Mutex
	wg               sync.WaitGroup
	cancel           context.CancelFunc
	// globalErrorCount counts consecutive network errors across all workers.
	// When it exceeds the threshold, a Tor circuit renewal is requested.
	globalErrorCount atomic.Int32
	transports       []*http.Transport
	transportsMu     sync.Mutex
}

func NewEngine(db *database.DB, proxyAddr string, workerCount int, maxDepth int) *Engine {
	return &Engine{
		DB:               db,
		Proxy:            proxyAddr,
		Workers:          workerCount,
		MaxDepth:         maxDepth,
		domainLastAccess: make(map[string]time.Time),
		transports:       make([]*http.Transport, workerCount),
	}
}

// Start starts the crawling engine with the specified number of workers.
// Each worker automatically restarts if it exits due to a Tor error.
func (e *Engine) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	log.Printf("🚀 Starting crawling engine with %d workers (MaxDepth: %d, Politeness Active)...", e.Workers, e.MaxDepth)
	for i := 0; i < e.Workers; i++ {
		e.wg.Add(1)
		go func(id int) {
			defer e.wg.Done()
			for {
				e.worker(ctx, id)
				select {
				case <-ctx.Done():
					log.Printf("[Worker %d] Stopped permanently.", id)
					return
				case <-time.After(10 * time.Second):
					log.Printf("[Worker %d] Restarting after Tor error...", id)
				}
			}
		}(i)
	}

	// Cleanup goroutine for domainLastAccess — prevents memory leak with thousands of domains
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cutoff := time.Now().Add(-10 * time.Minute)
				e.domainMu.Lock()
				for host, t := range e.domainLastAccess {
					if t.Before(cutoff) {
						delete(e.domainLastAccess, host)
					}
				}
				e.domainMu.Unlock()
			}
		}
	}()
}

// Stop shuts down all workers and waits for them to finish
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	log.Println("🛑 Crawling engine stopped.")
}

// waitForDomain ensures we don't hammer the same domain — enforces a 5-second delay.
// Returns false if the context was cancelled while waiting.
func (e *Engine) waitForDomain(ctx context.Context, targetUrl string) bool {
	parsed, err := url.Parse(targetUrl)
	if err != nil {
		return true
	}
	host := parsed.Host

	e.domainMu.Lock()
	lastAccess, exists := e.domainLastAccess[host]

	if exists {
		elapsed := time.Since(lastAccess)
		if elapsed < 5*time.Second {
			waitTime := 5*time.Second - elapsed
			e.domainLastAccess[host] = time.Now().Add(waitTime)
			e.domainMu.Unlock()
			select {
			case <-ctx.Done():
				return false
			case <-time.After(waitTime):
			}
			e.domainMu.Lock()
			e.domainLastAccess[host] = time.Now()
			e.domainMu.Unlock()
			return true
		}
	}

	e.domainLastAccess[host] = time.Now()
	e.domainMu.Unlock()
	return true
}

func (e *Engine) worker(ctx context.Context, id int) {
	log.Printf("[Worker %d] Active", id)

	transport, client, err := proxy.NewTorClientWithTransport(e.Proxy)
	if err != nil {
		log.Printf("[Worker %d] Fatal Tor error: %v", id, err)
		return
	}

	// Replace the transport at the worker's index (not append) — prevents memory leak on restart.
	e.transportsMu.Lock()
	e.transports[id] = transport
	e.transportsMu.Unlock()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[Worker %d] Stopped.", id)
			return
		default:
		}

		targetUrl, depth, userID, err := e.DB.GetNextPendingNode()
		if err != nil {
			log.Printf("[Worker %d] Error fetching from queue: %v", id, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		if targetUrl == "" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
			continue
		}

		// Crawl-time validation: we process exclusively .onion addresses
		if parsedTarget, err := url.Parse(targetUrl); err != nil || !strings.HasSuffix(strings.ToLower(parsedTarget.Hostname()), ".onion") {
			log.Printf("[Worker %d] Non-.onion URL in DB, marked as invalid: %s", id, logScrub(targetUrl))
			_ = e.DB.MarkRobotsBlocked(targetUrl, userID)
			continue
		}

		log.Printf("[Worker %d] Waiting for rate-limit permission for: %s", id, logScrub(targetUrl))
		if !e.waitForDomain(ctx, targetUrl) {
			return // context cancelled while waiting
		}
		log.Printf("[Worker %d] Crawl approved: %s (Depth: %d, User: %d)", id, logScrub(targetUrl), depth, userID)

		// Check robots.txt before scraping
		if !IsAllowed(ctx, client, targetUrl) {
			log.Printf("[Worker %d] Blocked by robots.txt: %s", id, logScrub(targetUrl))
			if err := e.DB.MarkRobotsBlocked(targetUrl, userID); err != nil {
				log.Printf("[Worker %d] DB Error marking robots blocked: %v", id, err)
			}
			continue
		}

		result, err := ScrapePage(ctx, client, targetUrl)
		if err != nil {
			log.Printf("[Worker %d] Network/SOCKS error at %s: %v", id, logScrub(targetUrl), err)
			if errRetry := e.DB.FailNodeWithRetry(targetUrl, userID); errRetry != nil {
				log.Printf("[Worker %d] DB Error on retry for %s: %v", id, logScrub(targetUrl), errRetry)
			}
			e.onNetworkError()
			continue
		}
		// Success — reset the error counter
		e.globalErrorCount.Store(0)
		changed, err := e.DB.SaveNode(targetUrl, result.Title, result.ServerHeader, result.StatusCode, "completed", result.Metadata, result.Content, result.Category, userID)
		if err != nil {
			log.Printf("[Worker %d] Error saving node: %v", id, err)
			if retryErr := e.DB.FailNodeWithRetry(targetUrl, userID); retryErr != nil {
				log.Printf("[Worker %d] Error on retry after SaveNode failure: %v", id, retryErr)
			}
		} else if !changed {
			log.Printf("[Worker %d] Content unchanged (identical hash): %s", id, logScrub(targetUrl))
		}

		if depth < e.MaxDepth {
			for _, foundUrl := range result.FoundOnions {
				if err = e.DB.SaveEdge(targetUrl, foundUrl, depth+1, userID); err != nil {
					log.Printf("[Worker %d] Error saving edge: %v", id, err)
				}
			}
			// At depth=0 we also fetch sitemap.xml for additional discovery
			if depth == 0 {
				sitemapURLs := FetchSitemapURLs(ctx, client, targetUrl)
				for _, su := range sitemapURLs {
					if err = e.DB.SaveEdge(targetUrl, su, 1, userID); err != nil {
						log.Printf("[Worker %d] Sitemap edge error: %v", id, err)
					}
				}
				if len(sitemapURLs) > 0 {
					log.Printf("[Worker %d] Sitemap: %d additional URLs from %s", id, len(sitemapURLs), logScrub(targetUrl))
				}
			}
			log.Printf("[Worker %d] Completed: %s (found %d new links)", id, logScrub(targetUrl), len(result.FoundOnions))
		} else {
			log.Printf("[Worker %d] Completed: %s (max depth %d reached, ignoring %d new links)", id, logScrub(targetUrl), e.MaxDepth, len(result.FoundOnions))
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Second):
		}
	}
}

// onNetworkError increments the global error counter and requests circuit renewal
// if the threshold of 5 consecutive errors has been exceeded.
func (e *Engine) onNetworkError() {
	count := e.globalErrorCount.Add(1)
	const threshold = 5
	if count < threshold || e.TorCtrl == nil {
		return
	}
	renewed, err := e.TorCtrl.RenewCircuit()
	if err != nil {
		log.Printf("[TorCtrl] Error renewing circuit: %v", err)
		return
	}
	if renewed {
		e.globalErrorCount.Store(0)
		// Close idle connections — the old circuit is no longer valid
		e.transportsMu.Lock()
		for _, t := range e.transports {
			if t != nil {
				t.CloseIdleConnections()
			}
		}
		e.transportsMu.Unlock()
	}
}
// AddToQueue manually adds a URL to the queue without overwriting existing data
func (e *Engine) AddToQueue(rawURL string, userID int) error {
	return e.DB.EnqueueURL(rawURL, 0, userID)
}

// logScrub strips CR/LF from a string before logging — strings.ReplaceAll on
// "\r" and "\n" is the exact sanitizer CodeQL's go/log-injection query
// accepts. See the same-named helper in the api package for the longer
// explanation.
func logScrub(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}
