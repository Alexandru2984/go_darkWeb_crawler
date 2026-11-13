package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"onion-spider/internal/database"
)

func TestSavedSearchAPIScopesUsersAndAdministrators(t *testing.T) {
	h, db := newAPI(t)
	_, a := mkUser(t, db, "a@example.com", "user")
	_, b := mkUser(t, db, "b@example.com", "user")
	_, admin := mkUser(t, db, "admin@example.com", "admin")
	for _, endpoint := range []struct{ method, path string }{{"GET", "/api/saved-searches"}, {"POST", "/api/saved-searches"}, {"POST", "/api/saved-searches/1"}, {"DELETE", "/api/saved-searches/1"}} {
		if rr := do(t, h, endpoint.method, endpoint.path, ""); rr.Code != 401 {
			t.Fatal("anonymous preferences access accepted")
		}
	}
	body := `{"name":"My private search","q":"private research","category":"wiki","status":"completed"}`
	rr := doWithBody(t, h, "POST", "/api/saved-searches", a, strings.NewReader(body))
	if rr.Code != 201 || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create: %d", rr.Code)
	}
	var saved database.SavedSearch
	if err := json.Unmarshal(rr.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/saved-searches/%d", saved.ID)
	for _, token := range []string{b, admin} {
		rr = do(t, h, "GET", "/api/saved-searches", token)
		if rr.Code != 200 || strings.Contains(rr.Body.String(), "private research") {
			t.Fatal("another account or admin saw private searches")
		}
		rr = doWithBody(t, h, "POST", path, token, strings.NewReader(`{"name":"Stolen"}`))
		if rr.Code != 404 {
			t.Fatal("another account or admin renamed private search")
		}
		if rr := do(t, h, "DELETE", path, token); rr.Code != 404 {
			t.Fatal("another account or admin deleted private search")
		}
	}
	for _, invalid := range []string{`{"name":"Bad","user_id":2}`, `{"name":"Bad","role":"admin"}`, `{"name":"Bad"} {"name":"Second"}`} {
		if rr := doWithBody(t, h, "POST", "/api/saved-searches", a, strings.NewReader(invalid)); rr.Code != 400 {
			t.Fatal("unknown fields or multiple documents accepted")
		}
	}
	if rr := doWithBody(t, h, "POST", "/api/saved-searches", a, strings.NewReader(body)); rr.Code != 409 {
		t.Fatal("duplicate name accepted")
	}
	if rr := doWithBody(t, h, "POST", "/api/saved-searches", b, strings.NewReader(body)); rr.Code != 201 {
		t.Fatal("another account's duplicate name blocked")
	}
	rr = doWithBody(t, h, "POST", path, a, strings.NewReader(`{"name":"Renamed"}`))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"q":"private research"`) {
		t.Fatal("rename lost criteria")
	}
	if rr := do(t, h, "DELETE", path, a); rr.Code != 200 {
		t.Fatal("owner delete failed")
	}
	if rr := do(t, h, "DELETE", path, a); rr.Code != 404 {
		t.Fatal("delete replay was not scoped")
	}
}

func TestSavedSearchExportAndReauthenticatedPurge(t *testing.T) {
	h, db := newAPI(t)
	const password = "Fixture-Saved-Search-9!"
	uid, token := mkVerifiedUser(t, db, "owner@example.com", password, "admin")
	other, _ := mkUser(t, db, "other@example.com", "user")
	for _, input := range []struct {
		id          int
		name, query string
	}{{uid, "Mine", "owned term"}, {other, "Other", "other account term"}} {
		if _, err := db.CreateSavedSearch(t.Context(), input.id, database.SavedSearchInput{Name: input.name, Query: input.query}); err != nil {
			t.Fatal(err)
		}
	}
	rr := do(t, h, "GET", "/api/privacy/export", token)
	if rr.Code != 200 {
		t.Fatalf("export: %d", rr.Code)
	}
	var exported struct {
		Version  int                    `json:"format_version"`
		Searches []database.SavedSearch `json:"saved_searches"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	if exported.Version != 5 || len(exported.Searches) != 1 || exported.Searches[0].Query != "owned term" || strings.Contains(rr.Body.String(), "other account term") {
		t.Fatal("personal export missing/scoping incorrect for administrator")
	}
	proof := `{"scope":"saved_searches","password":"wrong"}`
	if rr := doWithBody(t, h, "POST", "/api/privacy/purge", token, strings.NewReader(proof)); rr.Code != 403 {
		t.Fatal("unproved bulk erasure accepted")
	}
	proof = `{"scope":"saved_searches","password":"` + password + `"}`
	if rr := doWithBody(t, h, "POST", "/api/privacy/purge", token, strings.NewReader(proof)); rr.Code != 200 {
		t.Fatal("owner purge failed")
	}
	owned, err := db.ListSavedSearches(t.Context(), uid)
	if err != nil || len(owned) != 0 {
		t.Fatal("purge left saved searches")
	}
	untouched, err := db.ListSavedSearches(t.Context(), other)
	if err != nil || len(untouched) != 1 {
		t.Fatal("purge changed another account")
	}
}

func TestSearchUnicodeBoundsAndNoQueryInErrorLogs(t *testing.T) {
	h, db := newAPI(t)
	_, token := mkUser(t, db, "search@example.com", "user")
	for _, tc := range []struct {
		query  string
		status int
	}{{strings.Repeat("文", 200), 200}, {strings.Repeat("文", 201), 400}, {"bad\x00term", 400}} {
		body, _ := json.Marshal(map[string]string{"q": tc.query})
		if rr := doWithBody(t, h, "POST", "/api/search", token, bytes.NewReader(body)); rr.Code != tc.status {
			t.Fatalf("Unicode query status=%d want=%d", rr.Code, tc.status)
		}
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	if _, err := db.Conn.Exec(`ALTER TABLE nodes RENAME TO saved_fixture_nodes_unavailable`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := db.Conn.Exec(`ALTER TABLE saved_fixture_nodes_unavailable RENAME TO nodes`); err != nil {
			t.Error(err)
		}
	}()
	secret := "private-query-must-never-be-logged"
	body, _ := json.Marshal(map[string]string{"q": secret})
	if rr := doWithBody(t, h, "POST", "/api/search", token, bytes.NewReader(body)); rr.Code != 500 {
		t.Fatal("database failure fixture did not reach search handler")
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatal("query terms leaked into error logs")
	}
}

func TestSavedSearchPurgeRequiresEnrolledSecondFactor(t *testing.T) {
	h, db := newAPI(t)
	const password = "Fixture-Saved-MFA-9!"
	uid, token := mkVerifiedUser(t, db, "saved-mfa@example.com", password, "user")
	_, recovery := enrolTOTP(t, h, token, password)
	token = loginTOTPToken(t, h, "saved-mfa@example.com", password, recovery[0])
	if _, err := db.CreateSavedSearch(t.Context(), uid, database.SavedSearchInput{Name: "Keep until proved"}); err != nil {
		t.Fatal(err)
	}
	if rr := postJSON(t, h, "/api/privacy/purge", token, `{"scope":"saved_searches","password":"`+password+`"}`); rr.Code != 403 {
		t.Fatal("MFA purge accepted password alone")
	}
	if rr := postJSON(t, h, "/api/privacy/purge", token, `{"scope":"saved_searches","password":"`+password+`","code":"`+recovery[1]+`"}`); rr.Code != 200 {
		t.Fatal("MFA purge with full proof failed")
	}
}

func TestSavedSearchMutationLimitIsPerAccount(t *testing.T) {
	h, db := newAPI(t)
	_, a := mkUser(t, db, "limit-a@example.com", "user")
	_, b := mkUser(t, db, "limit-b@example.com", "user")
	for range 60 {
		if rr := postJSON(t, h, "/api/saved-searches", a, `{"unexpected":true}`); rr.Code != 400 {
			t.Fatalf("early limit status=%d", rr.Code)
		}
	}
	if rr := postJSON(t, h, "/api/saved-searches", a, `{"name":"Limited"}`); rr.Code != 429 {
		t.Fatal("mutation throttling missing")
	}
	if rr := postJSON(t, h, "/api/saved-searches", b, `{"name":"Independent"}`); rr.Code != 201 {
		t.Fatal("one account consumed another account's limit")
	}
}
