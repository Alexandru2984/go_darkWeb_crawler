package crawler

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// robotsCache este un LRU cache cu TTL pentru regulile robots.txt.
// Capacitate maxima: 500 domenii. Intrare expirata dupa 1 ora.
type robotsCache struct {
	mu      sync.Mutex
	entries map[string]*robotsEntry
	maxSize int
}

type robotsEntry struct {
	disallowed []string // prefixe de cai interzise pentru crawleri
	fetchedAt  time.Time
	ttl        time.Duration
}

var globalRobotsCache = &robotsCache{
	entries: make(map[string]*robotsEntry),
	maxSize: 500,
}

const robotsTTL = 1 * time.Hour
const robotsUA = "OnionSpider"

// IsAllowed verifica daca crawlerul are voie sa acceseze un URL conform robots.txt.
// Returneaza true daca e permis sau daca robots.txt nu a putut fi descarcat (fail-open).
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
		// Evict cel mai vechi entry daca am depasit capacitatea
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

// fetchRobots descarca si parseaza robots.txt pentru un host.
// Returneaza un entry gol (permit tot) daca nu exista sau eroare.
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
		return entry // fail-open: daca nu putem descarca, permitem
	}
	defer resp.Body.Close()

	entry.disallowed = parseRobots(resp, robotsUA)
	return entry
}

// parseRobots citeste un robots.txt si extrage caile interzise pentru UA-ul dat.
// Suporta sectiunile User-agent: * si User-agent: OnionSpider.
func parseRobots(resp *http.Response, ua string) []string {
	var disallowed []string
	applies := false
	scanner := bufio.NewScanner(resp.Body)

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
			if applies && value != "" {
				disallowed = append(disallowed, value)
			}
		}
	}
	return disallowed
}

// evictOldest sterge intrarea cu fetchedAt cel mai vechi din map (O(n), acceptabil pentru maxSize=500)
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
