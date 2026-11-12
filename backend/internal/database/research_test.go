package database

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func researchNode(t *testing.T, db *DB, uid int, url, title, category, date string) int {
	t.Helper()
	_, err := db.SaveNode(url, title, "", 200, "completed", "{}", "alpha research text", category, uid)
	if err != nil {
		t.Fatal(err)
	}
	var id int
	if err := db.Conn.QueryRow(`SELECT id FROM nodes WHERE user_id=$1 AND url=$2`, uid, url).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn.Exec(`UPDATE nodes SET discovered_at=$2::timestamp WHERE id=$1`, id, date); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestBrowseFiltersComposeBeforePaginationAndRespectOwnership(t *testing.T) {
	db := newTestDB(t)
	a := mustUser(t, db, "research-a@example.com")
	b := mustUser(t, db, "research-b@example.com")
	first := researchNode(t, db, a, testURLA, "Alpha 100%_ literal", "wiki", "2026-11-10 08:00:00")
	researchNode(t, db, a, testURLB, "Beta news", "news", "2026-11-11 23:59:59.999")
	last := researchNode(t, db, a, testURLA+"late", "Alpha other", "wiki", "2026-11-12 08:00:00")
	researchNode(t, db, b, testURLA, "Other secret", "wiki", "2026-11-10 08:00:00")
	for _, pair := range []struct {
		uid int
		url string
	}{{a, testURLA}, {a, testURLA + "late"}, {b, testURLA}} {
		if err := db.AddTag(pair.url, pair.uid, "private"); err != nil {
			t.Fatal(err)
		}
	}
	host := strings.Split(strings.TrimPrefix(testURLA, "http://"), "/")[0]
	input := BrowseInput{Query: "alpha", Category: "wiki", Status: "completed", ResearchFilters: ResearchFilters{Host: host, Tag: "private", From: "2026-11-10", To: "2026-11-12", Sort: "oldest"}}
	list, err := db.BrowseNodes(t.Context(), input, 1, 1, a, true)
	if err != nil || len(list) != 1 || list[0].ID != last {
		t.Fatalf("filter/page/owner mismatch: %v %v", list, err)
	}
	input.To = "2026-11-11"
	list, err = db.BrowseNodes(t.Context(), input, 20, 0, a, true)
	if err != nil || len(list) != 1 || list[0].ID != first {
		t.Fatal("date boundary or collection admin scope incorrect")
	}
	list, err = db.BrowseNodes(t.Context(), BrowseInput{ResearchFilters: ResearchFilters{To: "2026-11-11"}}, 20, 0, a, false)
	if err != nil || len(list) != 2 {
		t.Fatal("inclusive final discovery day omitted")
	}
	list, err = db.BrowseNodes(t.Context(), BrowseInput{ResearchFilters: ResearchFilters{Title: "100%_"}}, 20, 0, a, false)
	if err != nil || len(list) != 1 || list[0].ID != first {
		t.Fatal("title wildcard characters were not treated literally")
	}
	list, err = db.BrowseNodes(t.Context(), BrowseInput{ResearchFilters: ResearchFilters{Sort: "title"}}, 20, 0, a, false)
	if err != nil || len(list) != 3 || list[0].ID != first {
		t.Fatal("title order or owner scope incorrect")
	}
}

func TestCollectionRenameTracksSavedFiltersAndDeletionNeverBroadensThem(t *testing.T) {
	db := newTestDB(t)
	a := mustUser(t, db, "collection-a@example.com")
	b := mustUser(t, db, "collection-b@example.com")
	for _, uid := range []int{a, b} {
		researchNode(t, db, uid, testURLA, "Site", "wiki", "2026-11-10 00:00:00")
		if err := db.AddTag(testURLA, uid, "Original"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.CreateSavedSearch(t.Context(), uid, SavedSearchInput{Name: "Saved", ResearchFilters: ResearchFilters{Tag: "original", Title: "site", From: "2026-11-10", To: "2026-11-12", Sort: "title"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.RenameCollection(t.Context(), a, "original", "Renamed"); err != nil {
		t.Fatal(err)
	}
	owned, _ := db.ListSavedSearches(t.Context(), a)
	other, _ := db.ListSavedSearches(t.Context(), b)
	if owned[0].Tag != "renamed" || other[0].Tag != "original" || owned[0].Title != "site" || owned[0].To != "2026-11-12" {
		t.Fatal("rename lost saved criteria or changed another account")
	}
	if err := db.SetNote(testURLA, a, "keep note"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteCollection(t.Context(), a, "renamed"); err != nil {
		t.Fatal(err)
	}
	list, err := db.BrowseNodes(t.Context(), BrowseInput{ResearchFilters: ResearchFilters{Tag: owned[0].Tag}}, 20, 0, a, true)
	if err != nil || len(list) != 0 {
		t.Fatal("deleted collection silently broadened saved search")
	}
	annotation, err := db.GetAnnotation(testURLA, a)
	if err != nil || annotation.Note != "keep note" {
		t.Fatal("collection removal erased notes or node")
	}
	if _, err := db.DeleteCollection(t.Context(), a, "original"); !errors.Is(err, ErrCollectionMissing) {
		t.Fatal("foreign collection was visible")
	}
}

func TestConcurrentTagsRespectLastNodeSlotAndIdempotenceAtLimit(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "tag-race@example.com")
	researchNode(t, db, uid, testURLA, "Site", "wiki", "2026-11-10 00:00:00")
	for i := range MaxTagsPerNode - 1 {
		if err := db.AddTag(testURLA, uid, fmt.Sprintf("existing-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for i := range 2 {
		wait.Go(func() {
			<-start
			results <- db.AddTagContext(t.Context(), testURLA, uid, fmt.Sprintf("concurrent-%d", i))
		})
	}
	close(start)
	wait.Wait()
	close(results)
	ok, full := 0, 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrTooManyTags):
			full++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || full != 1 {
		t.Fatal("concurrent tag additions exceeded node cap")
	}
	if err := db.AddTag(testURLA, uid, "existing-0"); err != nil {
		t.Fatal("existing tag was not idempotent at capacity")
	}
}

func TestCollectionQuotaIsSerializedAcrossDifferentNodes(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "group-race@example.com")
	ids := make([]int, 5)
	for i := range 5 {
		ids[i] = researchNode(t, db, uid, testURLA+fmt.Sprintf("seed-%d", i), "Site", "wiki", "2026-11-10 00:00:00")
	}
	for i := range MaxCollections - 1 {
		if _, err := db.Conn.Exec(`INSERT INTO node_tags(user_id,node_id,tag) VALUES($1,$2,$3)`, uid, ids[i%5], fmt.Sprintf("seed-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	researchNode(t, db, uid, testURLB, "Target", "wiki", "2026-11-10 00:00:00")
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for i := range 2 {
		wait.Go(func() {
			<-start
			results <- db.AddTagContext(t.Context(), testURLB, uid, fmt.Sprintf("last-group-%d", i))
		})
	}
	close(start)
	wait.Wait()
	close(results)
	ok, full := 0, 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrCollectionLimit):
			full++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || full != 1 {
		t.Fatal("global collection quota race")
	}
	groups, err := db.ListTags(uid)
	if err != nil || len(groups) != MaxCollections {
		t.Fatal("bounded collection list incorrect")
	}
}

func TestResearchFilterValidation(t *testing.T) {
	for _, input := range []ResearchFilters{{Host: "not-an-onion"}, {Title: "bad\x00title"}, {Title: strings.Repeat("文", 201)}, {From: "2026-02-30"}, {From: "0000-01-01"}, {From: "2026-11-12", To: "2026-11-01"}, {Sort: "id; DROP TABLE users"}, {Tag: "bad\x00tag"}} {
		if _, err := NormalizeResearchFilters(input); err == nil {
			t.Fatal("invalid filters accepted")
		}
	}
	if _, err := NormalizeResearchFilters(ResearchFilters{Title: strings.Repeat("文", 200), Tag: strings.Repeat("ă", 40)}); err != nil {
		t.Fatal("valid Unicode filters rejected")
	}
}

func TestSaveNodeRepairsHostForUnchangedContent(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "host-repair@example.com")
	researchNode(t, db, uid, testURLA, "Title", "wiki", "2026-11-10 00:00:00")
	if _, err := db.Conn.Exec(`UPDATE nodes SET host=NULL WHERE user_id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveNode(testURLA, "Title", "", 200, "completed", "{}", "alpha research text", "wiki", uid); err != nil {
		t.Fatal(err)
	}
	var host string
	if err := db.Conn.QueryRow(`SELECT host FROM nodes WHERE user_id=$1`, uid).Scan(&host); err != nil || !strings.HasSuffix(host, ".onion") {
		t.Fatal("unchanged-content save left missing host")
	}
}

func TestCollectionRenameCollisionDoesNotMergeOrChangeSavedCriteria(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "rename-collision@example.com")
	for _, url := range []string{testURLA, testURLB} {
		researchNode(t, db, uid, url, "Title", "wiki", "2026-11-10 00:00:00")
	}
	if err := db.AddTag(testURLA, uid, "first"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddTag(testURLB, uid, "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateSavedSearch(t.Context(), uid, SavedSearchInput{Name: "First", ResearchFilters: ResearchFilters{Tag: "first"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RenameCollection(t.Context(), uid, "first", "second"); !errors.Is(err, ErrCollectionDuplicate) {
		t.Fatal("rename silently merged disjoint groups")
	}
	groups, _ := db.ListTags(uid)
	saved, _ := db.ListSavedSearches(t.Context(), uid)
	if len(groups) != 2 || saved[0].Tag != "first" {
		t.Fatal("failed rename partially modified data")
	}
}
