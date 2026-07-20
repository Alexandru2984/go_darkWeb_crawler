package crawler

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Workers that land on the same host must be spaced by the politeness delay.
//
// This is a regression test for a clobbered reservation: waitForDomain used to
// write time.Now() into the map after waking, which overwrote a later slot
// booked by another worker during the sleep. Two workers then came due at the
// same instant and hit the host together.
//
//	A hits at T, B reserves T+d, C sees T+d and reserves T+2d,
//	B wakes at T+d and writes T+d — C's reservation is erased, so D computes
//	from T+d, reserves T+2d, and fires simultaneously with C.
//
// The arrival order matters and has to be sequenced explicitly: simply starting
// four waiters at once does NOT reproduce it, because they all book their slots
// before the first one wakes, and the destructive write then lands after
// everyone already holds a correct reservation. The collision needs a worker
// arriving *after* a clobbering write, so D below is started deliberately late.
func TestWaitForDomain_SpacesConcurrentWorkers(t *testing.T) {
	const (
		delay    = 200 * time.Millisecond
		tolerate = 60 * time.Millisecond // scheduler jitter
	)

	e := &Engine{
		domainLastAccess: make(map[string]time.Time),
		domainDelay:      delay,
	}
	ctx := context.Background()
	const target = "http://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.onion/"

	var mu sync.Mutex
	hits := map[string]time.Time{}
	record := func(name string) {
		mu.Lock()
		hits[name] = time.Now()
		mu.Unlock()
	}

	// A primes the map and returns immediately.
	if !e.waitForDomain(ctx, target, 0) {
		t.Fatal("first call should not have waited")
	}

	var wg sync.WaitGroup
	start := func(name string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e.waitForDomain(ctx, target, 0) {
				record(name)
			}
		}()
	}

	start("B") // books ~T+200, wakes and (buggy build) writes T+200
	time.Sleep(delay / 20)
	start("C") // books ~T+400

	// Wait until after B has woken and done its damage, then bring in D. On the
	// buggy build D reads B's overwrite, computes from T+200, and books ~T+400 —
	// landing on top of C.
	time.Sleep(delay + delay/4)
	start("D")

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 3 {
		t.Fatalf("got %d hits, want 3", len(hits))
	}
	gap := hits["D"].Sub(hits["C"])
	if gap < 0 {
		gap = -gap
	}
	if gap < delay-tolerate {
		t.Errorf("C and D hit the same host %v apart, want at least %v — "+
			"a reservation was overwritten and two workers came due together", gap, delay)
	}
}

// A cancelled context must abandon the wait rather than sleeping it out, so
// shutdown is not held up by a worker parked on a slow host.
func TestWaitForDomain_AbortsOnCancelledContext(t *testing.T) {
	e := &Engine{
		domainLastAccess: make(map[string]time.Time),
		domainDelay:      2 * time.Second,
	}
	const target = "http://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.onion/"

	// First call primes the map so the second one has to wait.
	if !e.waitForDomain(context.Background(), target, 0) {
		t.Fatal("first call should not wait")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if e.waitForDomain(ctx, target, 0) {
		t.Error("waitForDomain returned true despite the context being cancelled")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waited %v after cancellation, expected to return promptly", elapsed)
	}
}

// A hostile or misconfigured robots.txt must not park a worker indefinitely.
// Asserted on the booked slot rather than by sleeping it out: actually waiting
// would cost maxDomainDelay (a minute) of test runtime to learn nothing extra.
func TestWaitForDomain_CapsOversizedCrawlDelay(t *testing.T) {
	e := &Engine{
		domainLastAccess: make(map[string]time.Time),
		domainDelay:      10 * time.Millisecond,
	}
	const (
		host   = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccc.onion"
		target = "http://" + host + "/"
	)

	if !e.waitForDomain(context.Background(), target, 0) {
		t.Fatal("first call should not wait")
	}

	// Ask for an hour. The slot the second caller books must be capped at
	// maxDomainDelay from now, not an hour out. Cancel the context so the call
	// returns as soon as it has booked, instead of serving the wait.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	e.waitForDomain(ctx, target, time.Hour)

	e.domainMu.Lock()
	booked := e.domainLastAccess[host]
	e.domainMu.Unlock()

	if ahead := booked.Sub(start); ahead > maxDomainDelay+time.Second {
		t.Errorf("booked slot is %v ahead, want no more than the %v cap", ahead, maxDomainDelay)
	}
}
