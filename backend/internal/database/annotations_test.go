package database

import (
	"context"
	"errors"
	"testing"
)

func mustCrawledNode(t *testing.T, db *DB, url string, userID int, content string) {
	t.Helper()
	if _, err := db.SaveNode(url, "a title", "nginx", 200, "completed", "", content, "market", userID); err != nil {
		t.Fatalf("SaveNode(%s): %v", url, err)
	}
}

// Annotations are private to the account that wrote them. Two accounts can hold
// the same .onion in their own graphs, and what one wrote about it must be
// invisible — and unreachable — to the other.
func TestAnnotationsAreScopedToTheAccountThatWroteThem(t *testing.T) {
	db := newTestDB(t)
	alice := mustUser(t, db, "alice@example.com")
	bob := mustUser(t, db, "bob@example.com")
	mustCrawledNode(t, db, testURLA, alice, "body")
	mustCrawledNode(t, db, testURLA, bob, "body")

	if err := db.AddTag(testURLA, alice, "investigating"); err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	if err := db.SetNote(testURLA, alice, "alice's private note"); err != nil {
		t.Fatalf("SetNote: %v", err)
	}

	got, err := db.GetAnnotation(testURLA, bob)
	if err != nil {
		t.Fatalf("GetAnnotation(bob): %v", err)
	}
	if len(got.Tags) != 0 {
		t.Errorf("bob sees alice's tags: %v", got.Tags)
	}
	if got.Note != "" {
		t.Errorf("bob sees alice's note: %q", got.Note)
	}

	// And alice still has hers.
	mine, err := db.GetAnnotation(testURLA, alice)
	if err != nil {
		t.Fatalf("GetAnnotation(alice): %v", err)
	}
	if len(mine.Tags) != 1 || mine.Tags[0] != "investigating" {
		t.Errorf("alice's tags = %v", mine.Tags)
	}
	if mine.Note != "alice's private note" {
		t.Errorf("alice's note = %q", mine.Note)
	}
}

// Annotating a site the account does not have must not distinguish "no such
// site" from "someone else's site": that difference is an oracle for which
// .onion addresses another account is tracking.
func TestAnnotatingAnotherAccountsSiteIsIndistinguishableFromMissing(t *testing.T) {
	db := newTestDB(t)
	owner := mustUser(t, db, "owner@example.com")
	stranger := mustUser(t, db, "stranger@example.com")
	mustCrawledNode(t, db, testURLA, owner, "body")

	// testURLA exists but belongs to owner; testURLB exists for nobody.
	for _, url := range []string{testURLA, testURLB} {
		if err := db.AddTag(url, stranger, "probe"); !errors.Is(err, ErrNodeNotFound) {
			t.Errorf("AddTag(%s) returned %v, want ErrNodeNotFound", url, err)
		}
		if _, err := db.GetAnnotation(url, stranger); !errors.Is(err, ErrNodeNotFound) {
			t.Errorf("GetAnnotation(%s) returned %v, want ErrNodeNotFound", url, err)
		}
		if _, err := db.StartWatch(url, stranger, 1); !errors.Is(err, ErrNodeNotFound) {
			t.Errorf("StartWatch(%s) returned %v, want ErrNodeNotFound", url, err)
		}
	}

	// Nothing was written to the owner's site.
	owned, err := db.GetAnnotation(testURLA, owner)
	if err != nil {
		t.Fatalf("GetAnnotation(owner): %v", err)
	}
	if len(owned.Tags) != 0 {
		t.Errorf("a stranger's tag landed on the owner's site: %v", owned.Tags)
	}
}

func TestTagsAreNormalisedAndDeduplicated(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "tags@example.com")
	mustCrawledNode(t, db, testURLA, uid, "body")

	for _, variant := range []string{"Market", "  market ", "market", "MARKET"} {
		if err := db.AddTag(testURLA, uid, variant); err != nil {
			t.Fatalf("AddTag(%q): %v", variant, err)
		}
	}
	got, err := db.GetAnnotation(testURLA, uid)
	if err != nil {
		t.Fatalf("GetAnnotation: %v", err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "market" {
		t.Errorf("tags = %v, want exactly [market]", got.Tags)
	}

	// Removing uses the same normalisation, so the tag a user typed removes the
	// tag they see.
	if err := db.RemoveTag(testURLA, uid, " MARKET "); err != nil {
		t.Fatalf("RemoveTag: %v", err)
	}
	got, err = db.GetAnnotation(testURLA, uid)
	if err != nil {
		t.Fatalf("GetAnnotation after remove: %v", err)
	}
	if len(got.Tags) != 0 {
		t.Errorf("tags after remove = %v, want none", got.Tags)
	}
}

func TestTagCountIsBounded(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "manytags@example.com")
	mustCrawledNode(t, db, testURLA, uid, "body")

	for i := 0; i < MaxTagsPerNode; i++ {
		if err := db.AddTag(testURLA, uid, "tag-"+string(rune('a'+i))); err != nil {
			t.Fatalf("AddTag %d: %v", i, err)
		}
	}
	if err := db.AddTag(testURLA, uid, "one-too-many"); !errors.Is(err, ErrTooManyTags) {
		t.Errorf("AddTag past the cap returned %v, want ErrTooManyTags", err)
	}
}

// Clearing a note deletes the row rather than storing an empty string, so the
// interface does not have to distinguish "no note" from "a note that is blank".
func TestClearingANoteRemovesIt(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "notes@example.com")
	mustCrawledNode(t, db, testURLA, uid, "body")

	if err := db.SetNote(testURLA, uid, "something"); err != nil {
		t.Fatalf("SetNote: %v", err)
	}
	if err := db.SetNote(testURLA, uid, "   "); err != nil {
		t.Fatalf("SetNote(blank): %v", err)
	}
	var rows int
	if err := db.Conn.QueryRow(`SELECT COUNT(*) FROM node_notes WHERE user_id = $1`, uid).Scan(&rows); err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if rows != 0 {
		t.Errorf("%d note rows remain after clearing, want 0", rows)
	}
}

// The first observation after starting a watch is the baseline. Reporting a
// change against the nothing we knew before would greet every new watch with an
// event the user did not ask about.
func TestFirstObservationEstablishesTheBaselineSilently(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "baseline@example.com")
	mustCrawledNode(t, db, testURLA, uid, "original body")
	if _, err := db.StartWatch(testURLA, uid, 1); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}

	if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
		t.Fatalf("RecordWatchObservation: %v", err)
	}
	events, err := db.ListWatchEvents(uid, 50)
	if err != nil {
		t.Fatalf("ListWatchEvents: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("baseline observation produced %d events, want 0: %+v", len(events), events)
	}
}

func TestWatchReportsAContentChangeExactlyOnce(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "changes@example.com")
	mustCrawledNode(t, db, testURLA, uid, "original body")
	if _, err := db.StartWatch(testURLA, uid, 1); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// The page changes.
	mustCrawledNode(t, db, testURLA, uid, "rewritten body")
	if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
		t.Fatalf("after change: %v", err)
	}
	events, err := db.ListWatchEvents(uid, 50)
	if err != nil {
		t.Fatalf("ListWatchEvents: %v", err)
	}
	if len(events) != 1 || events[0].Kind != WatchEventChanged {
		t.Fatalf("events = %+v, want one content_changed", events)
	}
	if events[0].URL != testURLA {
		t.Errorf("event names %q, want %q", events[0].URL, testURLA)
	}

	// A recrawl that finds the same content must not report it again.
	if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
		t.Fatalf("unchanged recrawl: %v", err)
	}
	events, err = db.ListWatchEvents(uid, 50)
	if err != nil {
		t.Fatalf("ListWatchEvents: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("an unchanged recrawl produced another event: %+v", events)
	}
}

// A site that has been down for a week should produce one entry, not one per
// crawl — and it must be able to report coming back.
func TestWatchReportsOnlyReachabilityTransitions(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "downup@example.com")
	mustCrawledNode(t, db, testURLA, uid, "body")
	if _, err := db.StartWatch(testURLA, uid, 1); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// Three failed crawls in a row.
	for i := 0; i < 3; i++ {
		if err := db.RecordWatchObservation(testURLA, uid, false, 0); err != nil {
			t.Fatalf("failure %d: %v", i, err)
		}
	}
	events, err := db.ListWatchEvents(uid, 50)
	if err != nil {
		t.Fatalf("ListWatchEvents: %v", err)
	}
	if len(events) != 1 || events[0].Kind != WatchEventUnreachable {
		t.Fatalf("events after three failures = %+v, want one unreachable", events)
	}

	// It answers again.
	if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	events, err = db.ListWatchEvents(uid, 50)
	if err != nil {
		t.Fatalf("ListWatchEvents: %v", err)
	}
	if len(events) != 2 || events[0].Kind != WatchEventRecovered {
		t.Fatalf("events after recovery = %+v, want a recovered on top", events)
	}
}

// A 5xx is "not answering" for the person watching, the same as a dead circuit.
func TestServerErrorCountsAsUnreachable(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "http503@example.com")
	mustCrawledNode(t, db, testURLA, uid, "body")
	if _, err := db.StartWatch(testURLA, uid, 1); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if err := db.RecordWatchObservation(testURLA, uid, true, 503); err != nil {
		t.Fatalf("503 observation: %v", err)
	}
	events, err := db.ListWatchEvents(uid, 50)
	if err != nil {
		t.Fatalf("ListWatchEvents: %v", err)
	}
	if len(events) != 1 || events[0].Kind != WatchEventUnreachable {
		t.Errorf("events = %+v, want one unreachable", events)
	}
	if events[0].StatusCode != 503 {
		t.Errorf("event status = %d, want 503", events[0].StatusCode)
	}
}

// A page that changes while the site is down must still be reported once it
// answers again. The watch deliberately does not advance its digest on a failed
// crawl, which is what makes that work.
func TestChangeDuringAnOutageIsReportedOnRecovery(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "outage@example.com")
	mustCrawledNode(t, db, testURLA, uid, "before")
	if _, err := db.StartWatch(testURLA, uid, 1); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if err := db.RecordWatchObservation(testURLA, uid, false, 0); err != nil {
		t.Fatalf("outage: %v", err)
	}

	// It comes back with different content.
	mustCrawledNode(t, db, testURLA, uid, "after")
	if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	events, err := db.ListWatchEvents(uid, 50)
	if err != nil {
		t.Fatalf("ListWatchEvents: %v", err)
	}
	kinds := map[string]int{}
	for _, e := range events {
		kinds[e.Kind]++
	}
	// Both, in the same pass. Reporting only the recovery and then advancing the
	// digest would swallow the change permanently — the watch would compare the
	// new text against itself forever after.
	if kinds[WatchEventRecovered] != 1 {
		t.Errorf("expected one recovered event, got %+v", events)
	}
	if kinds[WatchEventChanged] != 1 {
		t.Errorf("the change made during the outage was never reported: %+v", events)
	}

	// And it is not reported a second time on the next unchanged crawl.
	if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
		t.Fatalf("post-recovery: %v", err)
	}
	events, err = db.ListWatchEvents(uid, 50)
	if err != nil {
		t.Fatalf("ListWatchEvents: %v", err)
	}
	kinds = map[string]int{}
	for _, e := range events {
		kinds[e.Kind]++
	}
	if kinds[WatchEventChanged] != 1 {
		t.Errorf("the outage change was reported twice: %+v", events)
	}
}

// Marking the feed read must not reach into another account's events.
func TestMarkingTheFeedReadIsScopedToTheAccount(t *testing.T) {
	db := newTestDB(t)
	mine := mustUser(t, db, "mine@example.com")
	theirs := mustUser(t, db, "theirs@example.com")
	for _, uid := range []int{mine, theirs} {
		mustCrawledNode(t, db, testURLA, uid, "before")
		if _, err := db.StartWatch(testURLA, uid, 1); err != nil {
			t.Fatalf("StartWatch: %v", err)
		}
		if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
			t.Fatalf("baseline: %v", err)
		}
		mustCrawledNode(t, db, testURLA, uid, "after")
		if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
			t.Fatalf("change: %v", err)
		}
	}

	if _, err := db.MarkWatchEventsSeen(mine); err != nil {
		t.Fatalf("MarkWatchEventsSeen: %v", err)
	}
	if n, err := db.CountUnseenWatchEvents(mine); err != nil || n != 0 {
		t.Errorf("own unseen count = %d (err %v), want 0", n, err)
	}
	if n, err := db.CountUnseenWatchEvents(theirs); err != nil || n != 1 {
		t.Errorf("another account's unseen count = %d (err %v), want 1 — its feed was marked read", n, err)
	}
}

// Retention is a statement about crawl records the account stopped looking at.
// A tag, a note or a watch is the account saying this site matters — and unlike
// crawl data, the user's own writing cannot be regenerated by crawling again.
func TestRetentionNeverDeletesAnnotatedOrWatchedSites(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "keepmine@example.com")
	setRetention(t, db, uid, 1)

	mustAgedNode(t, db, testURLA, uid, 90) // tagged
	mustAgedNode(t, db, testURLB, uid, 90) // noted
	mustAgedNode(t, db, testURLC, uid, 90) // watched
	plain := testURLA + "plain"
	mustAgedNode(t, db, plain, uid, 90) // nothing attached

	if err := db.AddTag(testURLA, uid, "keep"); err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	if err := db.SetNote(testURLB, uid, "why this matters"); err != nil {
		t.Fatalf("SetNote: %v", err)
	}
	if _, err := db.StartWatch(testURLC, uid, 1); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}

	report, err := db.ApplyRetention(context.Background(), 100, false)
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if report.NodesDeleted != 1 {
		t.Errorf("retention deleted %d nodes, want only the unannotated one", report.NodesDeleted)
	}
	for _, url := range []string{testURLA, testURLB, testURLC} {
		var n int
		if err := db.Conn.QueryRow(
			`SELECT COUNT(*) FROM nodes WHERE url = $1 AND user_id = $2`, url, uid).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", url, err)
		}
		if n != 1 {
			t.Errorf("retention deleted an annotated or watched site: %s", url)
		}
	}
	var remaining int
	if err := db.Conn.QueryRow(
		`SELECT COUNT(*) FROM nodes WHERE url = $1 AND user_id = $2`, plain, uid).Scan(&remaining); err != nil {
		t.Fatalf("count plain: %v", err)
	}
	if remaining != 0 {
		t.Error("retention did not delete the unannotated record it was supposed to")
	}
}

// Deleting the account takes its writing with it.
func TestAccountDeletionRemovesAnnotationsAndWatches(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "goodbye@example.com")
	mustCrawledNode(t, db, testURLA, uid, "body")
	if err := db.AddTag(testURLA, uid, "keep"); err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	if err := db.SetNote(testURLA, uid, "note"); err != nil {
		t.Fatalf("SetNote: %v", err)
	}
	if _, err := db.StartWatch(testURLA, uid, 1); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	if err := db.RecordWatchObservation(testURLA, uid, true, 200); err != nil {
		t.Fatalf("observe: %v", err)
	}

	if err := db.DeleteAccount(uid, ""); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	for _, table := range []string{"node_tags", "node_notes", "watches", "watch_events"} {
		var n int
		if err := db.Conn.QueryRow(
			`SELECT COUNT(*) FROM ` + table + ` WHERE user_id = ` + itoa(uid)).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s survived account deletion: %d rows", table, n)
		}
	}
}

// itoa avoids importing strconv for one call in a table-driven query name. The
// value is an internal row id, never user input.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
