package api

import (
	"sync"
	"time"
)

// CrawlLimiter is a per-IP fixed-window rate limiter.
type CrawlLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*limitBucket
	limit      int
	window     time.Duration
	maxBuckets int
}

type limitBucket struct {
	count   int
	resetAt time.Time
}

// NewCrawlLimiter returns a limiter that allows `limit` requests per IP within
// `window`. The bucket map is capped at 100_000 entries and is periodically GC'd.
func NewCrawlLimiter(limit int, window time.Duration) *CrawlLimiter {
	l := &CrawlLimiter{
		buckets:    make(map[string]*limitBucket),
		limit:      limit,
		window:     window,
		maxBuckets: 100_000,
	}
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			l.mu.Lock()
			now := time.Now()
			for ip, b := range l.buckets {
				if now.After(b.resetAt) {
					delete(l.buckets, ip)
				}
			}
			l.mu.Unlock()
		}
	}()
	return l
}

// Allow records a request from `ip` and returns true if it is within the limit.
func (l *CrawlLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok || now.After(b.resetAt) {
		if !ok && len(l.buckets) >= l.maxBuckets {
			for k, v := range l.buckets {
				if now.After(v.resetAt) {
					delete(l.buckets, k)
				}
			}
			if len(l.buckets) >= l.maxBuckets {
				return false
			}
		}
		l.buckets[ip] = &limitBucket{count: 1, resetAt: now.Add(l.window)}
		return true
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	return true
}
