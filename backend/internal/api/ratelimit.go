package api

import (
	"net/http"
	"strconv"
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

// Allow records a request against `key` and returns true if it is within the
// limit. The key is an identity, not necessarily an address — see RequestKey.
func (l *CrawlLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[key]
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
		l.buckets[key] = &limitBucket{count: 1, resetAt: now.Add(l.window)}
		return true
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	return true
}

// RequestKey returns the identity a rate limit should be charged to.
//
// Authenticated requests are charged to the account. Charging them to an
// address instead breaks badly on the onion path, where nginx has no client
// address to forward and every Tor visitor arrives as 127.0.0.1: one visitor
// would spend the crawl and search quota of every other visitor. The account is
// also the fairer unit on clearnet, where a household or a corporate egress
// shares one address.
//
// Unauthenticated requests have no account yet, so they fall back to the
// address, namespaced by front door so onion and clearnet traffic can never
// consume each other's budget.
func RequestKey(r *http.Request) string {
	if uid := GetUserID(r); uid != 0 {
		return "user:" + strconv.Itoa(uid)
	}
	return ClientNetwork(r) + ":" + ClientIP(r)
}

// AnonLimiter rate-limits requests that have no account behind them yet: login,
// registration, verification and password reset.
//
// The onion path gets its own limiter rather than its own bucket. Every Tor
// visitor shares the single key there, so the clearnet limit — five login
// attempts a minute, sized for one person — would mean any visitor could lock
// every other visitor out of the login form by failing five times. The onion
// limit is therefore sized for the whole front door, and the per-account
// controls in the database (lockout after five failures for the same email,
// per-recipient caps on verification and reset mail) remain the defense that
// actually bounds an attack on a specific account. Those controls are unaffected
// by the source address, which is what makes the looser aggregate limit safe.
type AnonLimiter struct {
	clearnet *CrawlLimiter
	onion    *CrawlLimiter
}

// NewAnonLimiter builds a limiter allowing clearnetLimit requests per address
// per window on the clearnet path, and onionLimit requests per window shared
// across the whole onion path.
func NewAnonLimiter(clearnetLimit, onionLimit int, window time.Duration) *AnonLimiter {
	return &AnonLimiter{
		clearnet: NewCrawlLimiter(clearnetLimit, window),
		onion:    NewCrawlLimiter(onionLimit, window),
	}
}

// Allow charges one request and reports whether it is within the limit.
func (a *AnonLimiter) Allow(r *http.Request) bool {
	if ClientNetwork(r) == networkOnion {
		return a.onion.Allow(networkOnion)
	}
	return a.clearnet.Allow(ClientIP(r))
}
