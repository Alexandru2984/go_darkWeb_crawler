package api

import (
	"encoding/json"
	"fmt"
	"onion-spider/internal/database"
	"strings"
	"testing"
)

func TestBookmarkAndFeedAPIPrivacyBoundaries(t *testing.T) {
	h, db := newAPI(t)
	uid, token := mkUser(t, db, "workflow@example.com", "user")
	_, admin := mkUser(t, db, "admin@example.com", "admin")
	if _, err := db.SaveNode(urlA, "Private title", "", 200, "completed", "{}", "body", "wiki", uid); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/bookmarks", "/api/watch/events/query"} {
		if rr := postJSON(t, h, path, "", `{}`); rr.Code != 401 {
			t.Fatal("anonymous access")
		}
	}
	target := `{"url":"` + urlA + `"}`
	if rr := postJSON(t, h, "/api/bookmarks", admin, target); rr.Code != 404 {
		t.Fatal("admin could bookmark another graph")
	}
	if rr := postJSON(t, h, "/api/bookmarks", token, target); rr.Code != 200 || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("bookmark failed", rr.Code)
	}
	if rr := postJSON(t, h, "/api/bookmarks", token, `{"url":"`+urlA+`","review":"follow_up"}`); rr.Code != 200 {
		t.Fatal("review failed")
	}
	escaped := `{"url":"` + urlA + `","body":"` + strings.Repeat(`\ud83e\uddc5`, 4000) + `"}`
	if rr := postJSON(t, h, "/api/annotations/note", token, escaped); rr.Code != 200 {
		t.Fatal("escaped Unicode note rejected", rr.Code)
	}
	note := strings.Repeat("🧅", 4000)
	body, _ := json.Marshal(map[string]string{"url": urlA, "body": note})
	if rr := postJSON(t, h, "/api/annotations/note", token, string(body)); rr.Code != 200 {
		t.Fatal("Unicode note rejected", rr.Code)
	}
	for _, bad := range []string{`{"url":"` + urlA + `","review":"owner"}`, `{"url":"` + urlA + `","user_id":1}`, `{"url":"` + urlA + `","remove":true,"review":"reviewed"}`} {
		if rr := postJSON(t, h, "/api/bookmarks", token, bad); rr.Code != 400 {
			t.Fatal("invalid mutation accepted", rr.Code)
		}
	}
	watch, err := db.StartWatch(urlA, uid, 1)
	if err != nil {
		t.Fatal(err)
	}
	var eventID int
	if err := db.Conn.QueryRow(`INSERT INTO watch_events(user_id,watch_id,kind) VALUES($1,$2,'content_changed') RETURNING id`, uid, watch.ID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{}`, `{"ids":[]}`, `{"ids":[0]}`, `{"ids":[2147483648]}`, `{"ids":[9223372036854775807]}`, `{"ids":[1],"all":true}`, `{"ids":null}`} {
		if rr := postJSON(t, h, "/api/watch/events/seen", token, bad); rr.Code != 400 {
			t.Fatal("ambiguous mark-read accepted", rr.Code)
		}
	}
	if n, _ := db.CountUnseenWatchEvents(uid); n != 1 {
		t.Fatal("invalid selection marked events")
	}
	if rr := postJSON(t, h, "/api/watch/events/seen", admin, fmt.Sprintf(`{"ids":[%d]}`, eventID)); rr.Code != 200 {
		t.Fatal("scoped selection failed")
	}
	if n, _ := db.CountUnseenWatchEvents(uid); n != 1 {
		t.Fatal("foreign selection marked events")
	}
	if rr := postJSON(t, h, "/api/watch/events/query?limit=20", token, `{"unread_only":true,"title":"Private","kind":"content_changed"}`); rr.Code != 200 || !strings.Contains(rr.Body.String(), "Private title") {
		t.Fatal("filtered feed failed", rr.Code)
	}
	if rr := postJSON(t, h, "/api/watch/events/seen", token, fmt.Sprintf(`{"ids":[%d]}`, eventID)); rr.Code != 200 {
		t.Fatal("selected read failed")
	}
	if n, _ := db.CountUnseenWatchEvents(uid); n != 0 {
		t.Fatal("own selection not marked")
	}
}

func TestWorkflowExportAndReauthenticatedPurge(t *testing.T) {
	h, db := newAPI(t)
	const password = "Workflow-Fixture-Password-9!"
	uid, token := mkVerifiedUser(t, db, "export@example.com", password, "user")
	if _, err := db.SaveNode(urlA, "Owned title", "", 200, "completed", "{}", "body", "wiki", uid); err != nil {
		t.Fatal(err)
	}
	if err := db.SetBookmark(t.Context(), uid, urlA, false); err != nil {
		t.Fatal(err)
	}
	watch, err := db.StartWatch(urlA, uid, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn.Exec(`INSERT INTO watch_events(user_id,watch_id,kind) SELECT $1,$2,'recovered' FROM generate_series(1,205)`, uid, watch.ID); err != nil {
		t.Fatal(err)
	}
	rr := do(t, h, "GET", "/api/privacy/export", token)
	var data struct {
		Version   int                          `json:"format_version"`
		Bookmarks []database.BookmarkReference `json:"bookmarks"`
		Events    []database.WatchEvent        `json:"watch_events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil || rr.Code != 200 || data.Version != 4 || len(data.Bookmarks) != 1 || len(data.Events) != 205 {
		t.Fatalf("incomplete export v4: %d %v", rr.Code, err)
	}
	for _, scope := range []string{"bookmarks", "watch_events"} {
		if rr := postJSON(t, h, "/api/privacy/purge", token, `{"scope":"`+scope+`","password":"wrong"}`); rr.Code != 403 {
			t.Fatal("purge without password")
		}
		if rr := postJSON(t, h, "/api/privacy/purge", token, `{"scope":"`+scope+`","password":"`+password+`"}`); rr.Code != 200 {
			t.Fatal("purge failed", rr.Code)
		}
	}
	_, recovery := enrolTOTP(t, h, token, password)
	token = loginTOTPToken(t, h, "export@example.com", password, recovery[0])
	for i, scope := range []string{"bookmarks", "watch_events"} {
		if rr := postJSON(t, h, "/api/privacy/purge", token, `{"scope":"`+scope+`","password":"`+password+`"}`); rr.Code != 403 {
			t.Fatal("MFA scope accepted password alone")
		}
		if rr := postJSON(t, h, "/api/privacy/purge", token, `{"scope":"`+scope+`","password":"`+password+`","code":"`+recovery[i+1]+`"}`); rr.Code != 200 {
			t.Fatal("MFA scope rejected full proof", rr.Code)
		}
	}
	watches, _ := db.ListWatches(uid)
	if len(watches) != 1 {
		t.Fatal("history purge removed watch")
	}
}
