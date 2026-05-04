package crawler

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// robotsCache is an LRU cache with TTL for robots.txt rules.
// Maximum capacity: 500 domains. Entries expire after 1 hour.
type robotsCache struct {
	mu      sync.Mutex
	entries map[string]*robotsEntry
	maxSize int
}

type robotsEntry struct {
	disallowed []string // path prefixes disallowed for crawlers
	fetchedAt  time.Time
	ttl        time.Duration
}

var globalRobotsCache = &robotsCache{
	entries: make(map[string]*robotsEntry),
	maxSize: 500,
}

const robotsTTL = 1 * time.Hour
const robotsUA = "OnionSpider"

// IsAllowed checks whether the crawler is allowed to access a URL according to robots.txt.
// Returns true if allowed or if robots.txt could not be fetched (fail-open).
func IsAllowed(ctx context.Context, client *http.Client, targetURL string) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return true
	}
	host := parsed.Host

	globalRobotsCache.mu.Lock()
	entry, ok := globalRobotsCache.entries[host]
	if ok && time.Since(entry.fetchedAt) > entry.ttl {
		delete(globalRobotsCache.entries, host)
		ok = false
	}
	globalRobotsCache.mu.Unlock()

	if !ok {
		entry = fetchRobots(ctx, client, parsed)
		globalRobotsCache.mu.Lock()
		// Evict the oldest entry if capacity has been exceeded
		if len(globalRobotsCache.entries) >= globalRobotsCache.maxSize {
			evictOldest(globalRobotsCache.entries)
		}
		globalRobotsCache.entries[host] = entry
		globalRobotsCache.mu.Unlock()
	}

	path := parsed.Path
	if path == "" {
		path = "/"
	}
	for _, prefix := range entry.disallowed {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}
	return true
}

// fetchRobots downloads and parses robots.txt for a host.
// Returns an empty entry (allow all) if it doesn't exist or on error.
func fetchRobots(ctx context.Context, client *http.Client, base *url.URL) *robotsEntry {
	entry := &robotsEntry{fetchedAt: time.Now(), ttl: robotsTTL}

	robotsURL := fmt.Sprintf("%s://%s/robots.txt", base.Scheme, base.Host)
	req, err := http.NewRequestWithContext(ctx, "GET", robotsURL, nil)
	if err != nil {
		return entry
	}
	req.Header.Set("User-Agent", robotsUA)

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return entry // fail-open: if we can't download, we allow
	}
	defer resp.Body.Close()

	entry.disallowed = parseRobots(io.LimitReader(resp.Body, 64*1024), robotsUA)
	return entry
}

// parseRobots reads a robots.txt and extracts disallowed paths for the given UA.
// Supports User-agent: * and User-agent: OnionSpider sections.
func parseRobots(body io.Reader, ua string) []string {
	var disallowed []string
	applies := false
	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		field := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch strings.ToLower(field) {
		case "user-agent":
			applies = value == "*" || strings.EqualFold(value, ua)
		case "disallow":
			if applies && value != "" && len(disallowed) < 1000 {
				disallowed = append(disallowed, value)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[robots.txt] Scan error: %v", err)
	}
	return disallowed
}

// evictOldest removes the entry with the oldest fetchedAt from the map (O(n), acceptable for maxSize=500)
func evictOldest(m map[string]*robotsEntry) {
	var oldestKey string
	var oldestTime time.Time
	for k, v := range m {
		if oldestKey == "" || v.fetchedAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = v.fetchedAt
		}
	}
	if oldestKey != "" {
		delete(m, oldestKey)
	}
}
