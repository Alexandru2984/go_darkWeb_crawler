package api

import (
	"encoding/json"
	"strings"
	"testing"

	"onion-spider/internal/database"
)

func TestCollectionRoutesUsePrivateOwnershipAndStrictBodies(t *testing.T) {
	h, db := newAPI(t)
	a, aToken := mkUser(t, db, "group-a@example.com", "user")
	b, bToken := mkUser(t, db, "group-b@example.com", "user")
	_, admin := mkUser(t, db, "admin@example.com", "admin")
	for _, uid := range []int{a, b} {
		if _, err := db.SaveNode(urlA, "Private site", "", 200, "completed", "{}", "private research", "wiki", uid); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/api/browse", "/api/collections/add", "/api/collections/rename", "/api/collections/delete"} {
		if rr := postJSON(t, h, path, "", `{}`); rr.Code != 401 {
			t.Fatal("anonymous research endpoint exposed")
		}
	}
	body := `{"url":"` + urlA + `","tag":"Private Collection"}`
	if rr := postJSON(t, h, "/api/collections/add", aToken, body); rr.Code != 200 {
		t.Fatalf("create group: %d %s", rr.Code, rr.Body.String())
	}
	for _, token := range []string{bToken, admin} {
		if rr := do(t, h, "GET", "/api/tags", token); rr.Code != 200 || strings.Contains(rr.Body.String(), "private collection") {
			t.Fatal("private collection leaked")
		}
		if rr := postJSON(t, h, "/api/collections/rename", token, `{"tag":"private collection","name":"stolen"}`); rr.Code != 404 {
			t.Fatal("cross-account group rename accepted")
		}
		if rr := postJSON(t, h, "/api/collections/delete", token, `{"tag":"private collection"}`); rr.Code != 404 {
			t.Fatal("cross-account group delete accepted")
		}
	}
	if rr := postJSON(t, h, "/api/collections/add", admin, body); rr.Code != 404 {
		t.Fatal("admin annotated another account's global node")
	}
	for _, bad := range []string{`{"url":"` + urlA + `","tag":"x","user_id":2}`, `{"tag":"x"} {"tag":"y"}`} {
		if rr := postJSON(t, h, "/api/collections/add", aToken, bad); rr.Code != 400 {
			t.Fatal("unsafe group body accepted")
		}
	}
	if rr := postJSON(t, h, "/api/browse", bToken, `{"tag":"private collection"}`); rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatal("collection membership crossed accounts")
	}
	if rr := postJSON(t, h, "/api/collections/add", bToken, body); rr.Code != 200 {
		t.Fatal("same name in another account blocked")
	}
	if rr := postJSON(t, h, "/api/collections/rename", aToken, `{"tag":"private collection","name":"Renamed"}`); rr.Code != 200 {
		t.Fatal("own rename failed")
	}
	if rr := postJSON(t, h, "/api/collections/delete", aToken, `{"tag":"renamed"}`); rr.Code != 200 {
		t.Fatal("own removal failed")
	}
	if rr := do(t, h, "GET", "/api/tags", bToken); !strings.Contains(rr.Body.String(), "private collection") {
		t.Fatal("another account's group was removed")
	}
}

func TestResearchFilterSnapshotsExportAndCollectionPurge(t *testing.T) {
	h, db := newAPI(t)
	const password = "Fixture-Collection-Password-9!"
	uid, token := mkVerifiedUser(t, db, "research@example.com", password, "admin")
	other, _ := mkUser(t, db, "other@example.com", "user")
	for _, id := range []int{uid, other} {
		if _, err := db.SaveNode(urlA, "Needle title", "", 200, "completed", "{}", "research text", "wiki", id); err != nil {
			t.Fatal(err)
		}
		if err := db.AddTag(urlA, id, "work"); err != nil {
			t.Fatal(err)
		}
	}
	criteria := `{"name":"Research","title":"Needle","discovered_from":"2026-01-01","discovered_to":"2099-12-31","tag":"work","sort":"title"}`
	rr := postJSON(t, h, "/api/saved-searches", token, criteria)
	if rr.Code != 201 {
		t.Fatalf("save full snapshot: %d %s", rr.Code, rr.Body.String())
	}
	var saved database.SavedSearch
	if err := json.Unmarshal(rr.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Title != "Needle" || saved.Tag != "work" || saved.Sort != "title" || saved.To != "2099-12-31" {
		t.Fatal("filter snapshot did not round trip")
	}
	rr = postJSON(t, h, "/api/browse", token, `{"title":"Needle","tag":"work","sort":"title"}`)
	var nodes []database.Node
	if err := json.Unmarshal(rr.Body.Bytes(), &nodes); err != nil || len(nodes) != 1 {
		t.Fatal("admin collection filter did not constrain to own graph")
	}
	rr = do(t, h, "GET", "/api/privacy/export", token)
	var export struct {
		Version int                    `json:"format_version"`
		Groups  []database.TagCount    `json:"collections"`
		Saved   []database.SavedSearch `json:"saved_searches"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &export); err != nil {
		t.Fatal(err)
	}
	if export.Version != 3 || len(export.Groups) != 1 || export.Groups[0].Count != 1 || len(export.Saved) != 1 || export.Saved[0].Title != "Needle" {
		t.Fatal("private export omitted research preferences")
	}
	if rr := postJSON(t, h, "/api/privacy/purge", token, `{"scope":"collections","password":"wrong"}`); rr.Code != 403 {
		t.Fatal("unproved group purge accepted")
	}
	if rr := postJSON(t, h, "/api/privacy/purge", token, `{"scope":"collections","password":"`+password+`"}`); rr.Code != 200 {
		t.Fatal("proved group purge failed")
	}
	groups, _ := db.ListTags(uid)
	otherGroups, _ := db.ListTags(other)
	if len(groups) != 0 || len(otherGroups) != 1 {
		t.Fatal("purge scope incorrect")
	}
	kept, _ := db.ListSavedSearches(t.Context(), uid)
	if kept[0].Tag != "work" {
		t.Fatal("group purge silently broadened a saved search")
	}
}

func TestCollectionPurgeRequiresMFAWhenEnrolled(t *testing.T) {
	h, db := newAPI(t)
	const password = "Fixture-Collection-MFA-9!"
	uid, token := mkVerifiedUser(t, db, "collection-mfa@example.com", password, "user")
	if _, err := db.SaveNode(urlA, "Title", "", 200, "completed", "{}", "body", "wiki", uid); err != nil {
		t.Fatal(err)
	}
	if err := db.AddTag(urlA, uid, "kept"); err != nil {
		t.Fatal(err)
	}
	_, recovery := enrolTOTP(t, h, token, password)
	token = loginTOTPToken(t, h, "collection-mfa@example.com", password, recovery[0])
	if rr := postJSON(t, h, "/api/privacy/purge", token, `{"scope":"collections","password":"`+password+`"}`); rr.Code != 403 {
		t.Fatal("collection purge bypassed MFA")
	}
	if rr := postJSON(t, h, "/api/privacy/purge", token, `{"scope":"collections","password":"`+password+`","code":"`+recovery[1]+`"}`); rr.Code != 200 {
		t.Fatal("collection purge rejected full proof")
	}
}
