package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"onion-spider/internal/auth"
)

// onionRequest builds a request shaped the way the onion vhost proxies one:
// loopback peer, loopback client address, and the front-door marker.
func onionRequest(t *testing.T) *http.Request {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:41234"
	r.Header.Set("X-Real-IP", "127.0.0.1")
	r.Header.Set(networkHeader, networkOnion)
	return r
}

// clearnetRequest is the same for the TLS vhost: a real client address restored
// from Cloudflare, and the marker pinned to clearnet.
func clearnetRequest(t *testing.T, clientIP string) *http.Request {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:41234"
	r.Header.Set("X-Real-IP", clientIP)
	r.Header.Set(networkHeader, networkClearnet)
	return r
}

// resolve runs a request through TrustedRealIP, which is where the front door
// and the client address are decided, and hands back the request as handlers
// downstream would see it.
func resolve(t *testing.T, r *http.Request) *http.Request {
	t.Helper()
	var got *http.Request
	TrustedRealIP(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		got = req
	})).ServeHTTP(httptest.NewRecorder(), r)
	if got == nil {
		t.Fatal("TrustedRealIP did not call the next handler")
	}
	return got
}

func TestClientNetworkRequiresATrustedProxy(t *testing.T) {
	// A request that reached the API directly, claiming to be onion traffic.
	// Honouring it would let an outsider drain the budget shared by Tor users.
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "203.0.113.7:5555"
	r.Header.Set(networkHeader, networkOnion)

	if got := ClientNetwork(resolve(t, r)); got != networkClearnet {
		t.Fatalf("untrusted peer set its own front door: got %q, want %q", got, networkClearnet)
	}
}

func TestClientNetworkSurvivesTheClientIPRewrite(t *testing.T) {
	// TrustedRealIP overwrites RemoteAddr with the client address, so the front
	// door has to be recorded before that happens. On clearnet the rewritten
	// address is public and would fail a later trust check outright.
	got := ClientNetwork(resolve(t, clearnetRequest(t, "198.51.100.9")))
	if got != networkClearnet {
		t.Fatalf("clearnet request: got %q, want %q", got, networkClearnet)
	}
	if got := ClientNetwork(resolve(t, onionRequest(t))); got != networkOnion {
		t.Fatalf("onion request: got %q, want %q", got, networkOnion)
	}
}

func TestRequestKeyChargesAuthenticatedRequestsToTheAccount(t *testing.T) {
	// Two onion visitors are indistinguishable by address: both are 127.0.0.1.
	// Once logged in they must still get independent budgets, otherwise one
	// visitor's crawling consumes everyone else's.
	first := resolve(t, onionRequest(t))
	second := resolve(t, onionRequest(t))

	withUser := func(r *http.Request, uid int) *http.Request {
		return r.WithContext(context.WithValue(r.Context(), userContextKey, &auth.Claims{UserID: uid}))
	}

	keyA := RequestKey(withUser(first, 11))
	keyB := RequestKey(withUser(second, 22))
	if keyA == keyB {
		t.Fatalf("two onion accounts share a rate-limit key: both %q", keyA)
	}
	if keyA != "user:11" {
		t.Fatalf("authenticated key = %q, want %q", keyA, "user:11")
	}
}

func TestRequestKeyNamespacesUnauthenticatedTrafficByFrontDoor(t *testing.T) {
	// An anonymous onion visitor and an anonymous clearnet visitor that happens
	// to originate from loopback must not land in the same bucket.
	onion := RequestKey(resolve(t, onionRequest(t)))
	clearnet := RequestKey(resolve(t, clearnetRequest(t, "127.0.0.1")))
	if onion == clearnet {
		t.Fatalf("front doors collapsed into one key: %q", onion)
	}
	if onion != networkOnion+":127.0.0.1" {
		t.Fatalf("onion key = %q", onion)
	}
}

func TestAnonLimiterKeepsOneOnionVisitorFromLockingOutTheRest(t *testing.T) {
	// The regression this whole change exists for. With a per-address limiter,
	// the clearnet login budget of five is shared by every Tor visitor, so five
	// failures from anyone closed the login form for all of them.
	lim := NewAnonLimiter(5, 100, time.Minute)

	attacker := resolve(t, onionRequest(t))
	for i := 0; i < 5; i++ {
		if !lim.Allow(attacker) {
			t.Fatalf("attacker attempt %d rejected before the aggregate limit", i+1)
		}
	}

	victim := resolve(t, onionRequest(t))
	if !lim.Allow(victim) {
		t.Fatal("a second onion visitor was locked out by someone else's failed logins")
	}
}

func TestAnonLimiterBoundsTheOnionFrontDoorInAggregate(t *testing.T) {
	// Looser than clearnet, but not unbounded: the onion path still has a
	// ceiling, with per-account lockout doing the per-user work underneath.
	lim := NewAnonLimiter(5, 10, time.Minute)
	for i := 0; i < 10; i++ {
		if !lim.Allow(resolve(t, onionRequest(t))) {
			t.Fatalf("request %d rejected below the aggregate limit", i+1)
		}
	}
	if lim.Allow(resolve(t, onionRequest(t))) {
		t.Fatal("onion front door exceeded its aggregate limit")
	}
}

func TestAnonLimiterKeepsFrontDoorsFromSpendingEachOther(t *testing.T) {
	lim := NewAnonLimiter(1, 1, time.Minute)

	if !lim.Allow(resolve(t, onionRequest(t))) {
		t.Fatal("first onion request should be allowed")
	}
	if lim.Allow(resolve(t, onionRequest(t))) {
		t.Fatal("second onion request should be blocked")
	}
	// The onion budget is now spent. A clearnet visitor must be unaffected.
	if !lim.Allow(resolve(t, clearnetRequest(t, "198.51.100.9"))) {
		t.Fatal("exhausting the onion budget also blocked clearnet")
	}
}

func TestAnonLimiterStillSeparatesClearnetAddresses(t *testing.T) {
	lim := NewAnonLimiter(1, 100, time.Minute)
	if !lim.Allow(resolve(t, clearnetRequest(t, "198.51.100.9"))) {
		t.Fatal("first address should be allowed")
	}
	if lim.Allow(resolve(t, clearnetRequest(t, "198.51.100.9"))) {
		t.Fatal("same address should be blocked at its limit")
	}
	if !lim.Allow(resolve(t, clearnetRequest(t, "198.51.100.10"))) {
		t.Fatal("a different clearnet address should have its own budget")
	}
}

func TestAllowNIsAllOrNothing(t *testing.T) {
	// A batch that does not fit must consume nothing. Charging partially would
	// leave the caller rejected *and* poorer, and would let a rejected batch
	// still starve the caller's own later single requests.
	lim := NewCrawlLimiter(10, time.Minute)
	if !lim.AllowN("user:1", 8) {
		t.Fatal("batch of 8 should fit in a budget of 10")
	}
	if lim.AllowN("user:1", 5) {
		t.Fatal("batch of 5 should not fit in the remaining 2")
	}
	if !lim.AllowN("user:1", 2) {
		t.Fatal("rejected batch consumed budget: 2 should still fit")
	}
}

func TestAllowNRejectsBatchesLargerThanTheWholeBudget(t *testing.T) {
	lim := NewCrawlLimiter(10, time.Minute)
	if lim.AllowN("user:1", 11) {
		t.Fatal("a batch larger than the entire window budget should be rejected")
	}
	if !lim.AllowN("user:1", 10) {
		t.Fatal("the rejected oversized batch should not have reserved anything")
	}
}

func TestBulkSubmissionCannotOutrunTheSingleURLLimit(t *testing.T) {
	// The regression: bulk used to cost one unit regardless of size, so twenty
	// requests of twenty URLs bought 400 enqueues against a limit of 20.
	const perMinute = 20
	lim := NewCrawlLimiter(perMinute, time.Minute)

	enqueued := 0
	for i := 0; i < 20; i++ {
		if lim.AllowN("user:7", 20) {
			enqueued += 20
		}
	}
	if enqueued > perMinute {
		t.Fatalf("bulk submissions enqueued %d URLs against a limit of %d", enqueued, perMinute)
	}
}
