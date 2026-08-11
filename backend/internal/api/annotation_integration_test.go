package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"onion-spider/internal/database"
)

func mustNodeFor(t *testing.T, db *database.DB, url string, userID int) {
	t.Helper()
	if _, err := db.SaveNode(url, "a title", "nginx", 200, "completed", "", "body", "market", userID); err != nil {
		t.Fatalf("SaveNode: %v", err)
	}
}

// One account must not be able to read, write or even confirm the existence of
// another's annotations. Answering differently for "not yours" and "no such
// site" would turn these endpoints into an oracle for which .onion addresses
// somebody else is tracking, which is the one thing this service must not leak.
func TestAnnotationsCannotCrossAccounts(t *testing.T) {
	h, db := newAPI(t)
	ownerID, ownerToken := mkUser(t, db, "owner-ann@example.com", "user")
	_, strangerToken := mkUser(t, db, "stranger-ann@example.com", "user")
	mustNodeFor(t, db, urlA, ownerID)

	rr := postJSON(t, h, "/api/annotations/tag", ownerToken, `{"url":"`+urlA+`","tag":"secret-label"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("owner tagging own site: %d %s", rr.Code, rr.Body.String())
	}

	// urlA exists but belongs to the owner; urlB exists for nobody. Both answer
	// the same way.
	for _, target := range []string{urlA, urlB} {
		for _, c := range []struct{ name, path, body string }{
			{"read", "/api/annotations", `{"url":"` + target + `"}`},
			{"tag", "/api/annotations/tag", `{"url":"` + target + `","tag":"probe"}`},
			{"note", "/api/annotations/note", `{"url":"` + target + `","body":"probe"}`},
			{"watch", "/api/watch", `{"url":"` + target + `","interval_days":1}`},
		} {
			rr := postJSON(t, h, c.path, strangerToken, c.body)
			if rr.Code != http.StatusNotFound {
				t.Errorf("stranger %s on %s: got %d, want 404", c.name, target, rr.Code)
			}
		}
	}

	// The owner's annotation is untouched.
	rr = postJSON(t, h, "/api/annotations", ownerToken, `{"url":"`+urlA+`"}`)
	var got database.Annotation
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode annotation: %v", err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "secret-label" {
		t.Errorf("owner's tags = %v, want [secret-label]", got.Tags)
	}
}

// The whole annotation surface takes its target in a POST body. A .onion
// address in a request line reaches browser history, the Referer header on the
// next navigation and every proxy log in between.
func TestAnnotationEndpointsNeverTakeTheAddressInTheRequestLine(t *testing.T) {
	h, db := newAPI(t)
	uid, token := mkUser(t, db, "urlshape@example.com", "user")
	mustNodeFor(t, db, urlA, uid)

	// GET with the address in the query string must not be a way in.
	for _, path := range []string{
		"/api/annotations?url=" + urlA,
		"/api/annotations/tag?url=" + urlA + "&tag=x",
		"/api/watch?url=" + urlA,
	} {
		rr := do(t, h, "GET", path, token)
		if rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
			t.Errorf("GET %s answered %d — the address must not be accepted in the request line", path, rr.Code)
		}
	}
}

func TestTagsAndNotesRoundTripThroughTheAPI(t *testing.T) {
	h, db := newAPI(t)
	uid, token := mkUser(t, db, "roundtrip@example.com", "user")
	mustNodeFor(t, db, urlA, uid)

	rr := postJSON(t, h, "/api/annotations/tag", token, `{"url":"`+urlA+`","tag":"  Market  "}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("add tag: %d %s", rr.Code, rr.Body.String())
	}
	var a database.Annotation
	json.Unmarshal(rr.Body.Bytes(), &a)
	if len(a.Tags) != 1 || a.Tags[0] != "market" {
		t.Errorf("tags = %v, want the normalised [market]", a.Tags)
	}

	rr = postJSON(t, h, "/api/annotations/note", token, `{"url":"`+urlA+`","body":"worth revisiting"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("set note: %d %s", rr.Code, rr.Body.String())
	}
	json.Unmarshal(rr.Body.Bytes(), &a)
	if a.Note != "worth revisiting" {
		t.Errorf("note = %q", a.Note)
	}

	rr = do(t, h, "GET", "/api/tags", token)
	var tags []database.TagCount
	json.Unmarshal(rr.Body.Bytes(), &tags)
	if len(tags) != 1 || tags[0].Tag != "market" || tags[0].Count != 1 {
		t.Errorf("tag list = %+v", tags)
	}

	rr = postJSON(t, h, "/api/annotations/tag", token, `{"url":"`+urlA+`","tag":"market","remove":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("remove tag: %d %s", rr.Code, rr.Body.String())
	}
	json.Unmarshal(rr.Body.Bytes(), &a)
	if len(a.Tags) != 0 {
		t.Errorf("tags after removal = %v", a.Tags)
	}
}

func TestWatchFeedIsScopedAndClearable(t *testing.T) {
	h, db := newAPI(t)
	mineID, mineToken := mkUser(t, db, "feed-mine@example.com", "user")
	theirsID, theirsToken := mkUser(t, db, "feed-theirs@example.com", "user")

	for _, uid := range []int{mineID, theirsID} {
		mustNodeFor(t, db, urlA, uid)
		if _, err := db.StartWatch(urlA, uid, 1); err != nil {
			t.Fatalf("StartWatch: %v", err)
		}
		if err := db.RecordWatchObservation(urlA, uid, true, 200); err != nil {
			t.Fatalf("baseline: %v", err)
		}
		if _, err := db.SaveNode(urlA, "a title", "nginx", 200, "completed", "", "changed body", "market", uid); err != nil {
			t.Fatalf("SaveNode: %v", err)
		}
		if err := db.RecordWatchObservation(urlA, uid, true, 200); err != nil {
			t.Fatalf("change: %v", err)
		}
	}

	rr := do(t, h, "GET", "/api/watch/events", mineToken)
	var feed struct {
		Events []database.WatchEvent `json:"events"`
		Unseen int                   `json:"unseen"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode feed: %v", err)
	}
	if len(feed.Events) != 1 || feed.Unseen != 1 {
		t.Fatalf("feed = %d events, %d unseen, want 1/1", len(feed.Events), feed.Unseen)
	}

	if rr := postJSON(t, h, "/api/watch/events/seen", mineToken, `{}`); rr.Code != http.StatusOK {
		t.Fatalf("mark seen: %d", rr.Code)
	}

	// The other account's feed is untouched.
	rr = do(t, h, "GET", "/api/watch/events", theirsToken)
	json.Unmarshal(rr.Body.Bytes(), &feed)
	if feed.Unseen != 1 {
		t.Errorf("another account's unseen count = %d, want 1 — its feed was marked read", feed.Unseen)
	}
}

func TestWatchIntervalIsValidated(t *testing.T) {
	h, db := newAPI(t)
	uid, token := mkUser(t, db, "interval@example.com", "user")
	mustNodeFor(t, db, urlA, uid)

	for _, body := range []string{
		`{"url":"` + urlA + `","interval_days":0}`,   // 0 means "use the default"
		`{"url":"` + urlA + `","interval_days":365}`, // the documented maximum
	} {
		if rr := postJSON(t, h, "/api/watch", token, body); rr.Code != http.StatusOK {
			t.Errorf("watch %s: got %d, want 200", body, rr.Code)
		}
	}
	for _, body := range []string{
		`{"url":"` + urlA + `","interval_days":-1}`,
		`{"url":"` + urlA + `","interval_days":9999}`,
	} {
		if rr := postJSON(t, h, "/api/watch", token, body); rr.Code != http.StatusBadRequest {
			t.Errorf("watch %s: got %d, want 400", body, rr.Code)
		}
	}
}

func TestAnnotationEndpointsRejectUnauthenticatedCallers(t *testing.T) {
	h, _ := newAPI(t)
	for _, c := range []struct{ method, target string }{
		{"POST", "/api/annotations"},
		{"POST", "/api/annotations/tag"},
		{"POST", "/api/annotations/note"},
		{"GET", "/api/tags"},
		{"POST", "/api/watch"},
		{"GET", "/api/watches"},
		{"GET", "/api/watch/events"},
		{"POST", "/api/watch/events/seen"},
	} {
		rr := do(t, h, c.method, c.target, "")
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s unauthenticated returned %d, want 401", c.method, c.target, rr.Code)
		}
	}
}

// Annotations are the user's own words, so they belong in a personal-data
// export alongside the crawl records a machine produced.
func TestPersonalExportIncludesAnnotationsAndWatches(t *testing.T) {
	h, db := newAPI(t)
	uid, token := mkVerifiedUser(t, db, "export-ann@example.com", privacyPassword, "user")
	mustNodeFor(t, db, urlA, uid)
	if err := db.AddTag(urlA, uid, "keep"); err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	if err := db.SetNote(urlA, uid, "my own words"); err != nil {
		t.Fatalf("SetNote: %v", err)
	}
	if _, err := db.StartWatch(urlA, uid, 3); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}

	rr := do(t, h, "GET", "/api/privacy/export", token)
	if rr.Code != http.StatusOK {
		t.Fatalf("export returned %d: %s", rr.Code, rr.Body.String())
	}
	var doc struct {
		Watches     []database.Watch         `json:"watches"`
		Annotations []database.AnnotatedSite `json:"annotations"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("export is not valid JSON: %v\n%s", err, rr.Body.String())
	}
	if len(doc.Watches) != 1 || doc.Watches[0].IntervalDays != 3 {
		t.Errorf("watches in export = %+v", doc.Watches)
	}
	if len(doc.Annotations) != 1 {
		t.Fatalf("annotations in export = %+v", doc.Annotations)
	}
	if doc.Annotations[0].Note != "my own words" || len(doc.Annotations[0].Tags) != 1 {
		t.Errorf("annotation = %+v", doc.Annotations[0])
	}
}
