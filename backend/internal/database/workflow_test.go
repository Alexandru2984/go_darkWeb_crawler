package database

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestBookmarkOwnershipPrivateNotesSavedFiltersAndCascade(t *testing.T) {
	db := newTestDB(t)
	a := mustUser(t, db, "bookmark-a@example.com")
	b := mustUser(t, db, "bookmark-b@example.com")
	id := researchNode(t, db, a, testURLA, "Owned", "wiki", "2026-11-10 00:00:00")
	researchNode(t, db, b, testURLA, "Other", "wiki", "2026-11-10 00:00:00")
	if err := db.SetBookmark(t.Context(), a, testURLA, false); err != nil {
		t.Fatal(err)
	}
	if err := db.SetBookmarkReview(t.Context(), a, testURLA, "follow_up"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetBookmarkReview(t.Context(), b, testURLA, "reviewed"); !errors.Is(err, ErrBookmarkMissing) {
		t.Fatal("foreign bookmark changed")
	}
	if _, err := db.Conn.Exec(`INSERT INTO node_bookmarks(user_id,node_id) VALUES($1,$2)`, b, id); err == nil {
		t.Fatal("database accepted wrong owner")
	}
	if err := db.SetNote(testURLA, a, "Private 100%_ literal"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetNote(testURLA, b, "Other private"); err != nil {
		t.Fatal(err)
	}
	filters := ResearchFilters{Note: "100%_", Review: "follow_up", Sort: "bookmarked"}
	list, err := db.BrowseNodes(t.Context(), BrowseInput{ResearchFilters: filters}, 20, 0, a, true)
	if err != nil || len(list) != 1 || list[0].ID != id || !list[0].Owned || !list[0].Bookmarked || list[0].Review != "follow_up" || list[0].NotePreview != "Private 100%_ literal" {
		t.Fatalf("private browse: %v %v", list, err)
	}
	list, err = db.BrowseNodes(t.Context(), BrowseInput{ResearchFilters: ResearchFilters{Note: "private"}}, 20, 0, a, true)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatal("admin private-note filter escaped own graph")
	}
	saved, err := db.CreateSavedSearch(t.Context(), a, SavedSearchInput{Name: "Review", ResearchFilters: filters})
	if err != nil || !saved.BookmarkedOnly || saved.Note != "100%_" || saved.Review != "follow_up" {
		t.Fatal("saved workflow criteria lost", err)
	}
	annotation, err := db.GetAnnotation(testURLA, a)
	if err != nil || annotation.Bookmark == nil || annotation.Bookmark.Review != "follow_up" {
		t.Fatal("bookmark missing from annotation")
	}
	if n, err := db.PurgeBookmarks(t.Context(), b); err != nil || n != 0 {
		t.Fatal("foreign purge", err)
	}
	if _, err := db.Conn.Exec(`DELETE FROM nodes WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	refs, err := db.ListBookmarkReferences(t.Context(), a)
	if err != nil || len(refs) != 0 {
		t.Fatal("bookmark did not cascade")
	}
}

func TestBookmarkQuotaConcurrentAndIdempotent(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "bookmark-quota@example.com")
	// All fixture URLs share a real v3 host; only their local path differs.
	for i := range MaxBookmarks + 1 {
		researchNode(t, db, uid, fmt.Sprintf("%ssite-%d", testURLA, i), "Site", "wiki", "2026-11-10 00:00:00")
	}
	if _, err := db.Conn.Exec(`INSERT INTO node_bookmarks(user_id,node_id) SELECT user_id,id FROM nodes WHERE user_id=$1 ORDER BY id LIMIT $2`, uid, MaxBookmarks-1); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := range 2 {
		wait.Go(func() {
			<-start
			results <- db.SetBookmark(t.Context(), uid, fmt.Sprintf("%ssite-%d", testURLA, MaxBookmarks-1+i), false)
		})
	}
	close(start)
	wait.Wait()
	close(results)
	success, limited := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrBookmarkLimit) {
			limited++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || limited != 1 {
		t.Fatal("concurrent quota exceeded")
	}
	if err := db.SetBookmark(t.Context(), uid, testURLA+"site-0", false); err != nil {
		t.Fatal("idempotent create rejected at quota")
	}
}

func TestWatchFiltersSelectionAndUnboundedStreamingExport(t *testing.T) {
	db := newTestDB(t)
	a := mustUser(t, db, "feed-a@example.com")
	b := mustUser(t, db, "feed-b@example.com")
	ids := map[int]int{}
	for _, uid := range []int{a, b} {
		researchNode(t, db, uid, testURLA, "100%_ site", "wiki", "2026-11-10 00:00:00")
		watch, err := db.StartWatch(testURLA, uid, 1)
		if err != nil {
			t.Fatal(err)
		}
		ids[uid] = watch.ID
	}
	if err := db.AddTag(testURLA, a, "Review"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn.Exec(`INSERT INTO watch_events(user_id,watch_id,kind) SELECT $1,$2,'content_changed' FROM generate_series(1,205)`, a, ids[a]); err != nil {
		t.Fatal(err)
	}
	var foreign int
	if err := db.Conn.QueryRow(`INSERT INTO watch_events(user_id,watch_id,kind) VALUES($1,$2,'recovered') RETURNING id`, b, ids[b]).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	feed, err := db.QueryWatchEvents(t.Context(), a, WatchFeedInput{Title: "100%_", Tag: "review", Kind: WatchEventChanged, UnreadOnly: true}, 21, 20)
	if err != nil || len(feed) != 21 {
		t.Fatal("feed filter/pagination", err)
	}
	visible := []int{feed[0].ID, foreign}
	n, err := db.MarkSelectedWatchEventsSeen(t.Context(), a, visible)
	if err != nil || n != 1 {
		t.Fatal("selection escaped account", err)
	}
	unseen, _ := db.CountUnseenWatchEvents(a)
	otherUnseen, _ := db.CountUnseenWatchEvents(b)
	if unseen != 204 || otherUnseen != 1 {
		t.Fatal("unseen/new events were incorrectly marked")
	}
	for _, bad := range [][]int{nil, {}, {0}, make([]int, 201)} {
		if _, err := db.MarkSelectedWatchEventsSeen(t.Context(), a, bad); !errors.Is(err, ErrWatchSelection) {
			t.Fatal("bad selection accepted")
		}
	}
	exported := 0
	if err := db.ExportWatchEvents(t.Context(), a, func(e WatchEvent) error {
		exported++
		if e.ID == foreign {
			t.Fatal("foreign event exported")
		}
		return nil
	}); err != nil || exported != 205 {
		t.Fatalf("truncated export: %d %v", exported, err)
	}
	if n, err := db.PurgeWatchEvents(t.Context(), a); err != nil || n != 205 {
		t.Fatal("purge failed", err)
	}
	watches, err := db.ListWatches(a)
	if err != nil || len(watches) != 1 {
		t.Fatal("history purge removed watch")
	}
}

func TestWorkflowUnicodeAndInvalidValues(t *testing.T) {
	for _, s := range []string{strings.Repeat("🧅", 4000), strings.Repeat("ă", 4000)} {
		if _, err := NormalizeNoteText(s); err != nil {
			t.Fatal("Unicode note rejected")
		}
	}
	for _, s := range []string{strings.Repeat("ă", 4001), "null\x00byte", string([]byte{0xff})} {
		if _, err := NormalizeNoteText(s); !errors.Is(err, ErrNoteText) {
			t.Fatal("invalid note accepted")
		}
	}
	for _, f := range []ResearchFilters{{Note: strings.Repeat("ă", 201)}, {Note: "x\x00"}, {Review: "admin"}, {Sort: "DROP TABLE nodes"}} {
		if _, err := NormalizeResearchFilters(f); !errors.Is(err, ErrResearchFilters) {
			t.Fatal("invalid filter accepted")
		}
	}
}
