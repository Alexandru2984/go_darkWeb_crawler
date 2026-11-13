package api

import (
	"encoding/json"
	"fmt"
	"onion-spider/internal/database"
	"strings"
	"testing"
	"time"
)

func TestQueueControlsAlwaysBelongToCaller(t *testing.T) {
	h, db := newAPI(t)
	uid, token := mkUser(t, db, "queue@example.com", "user")
	_, admin := mkUser(t, db, "admin@example.com", "admin")
	if err := db.EnqueueURL(urlA, 0, uid, 10); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/queue/pause", "/api/queue/jobs", "/api/queue/action", "/api/crawl/bulk/preview"} {
		if rr := postJSON(t, h, path, "", `{}`); rr.Code != 401 {
			t.Fatal("anonymous queue access")
		}
	}
	for _, body := range []string{`{}`, `{"paused":null}`, `{"paused":true,"user_id":1}`, `{"paused":true} {}`} {
		if rr := postJSON(t, h, "/api/queue/pause", token, body); rr.Code != 400 {
			t.Fatal("ambiguous pause accepted", rr.Code)
		}
	}
	if rr := postJSON(t, h, "/api/queue/pause", admin, `{"paused":true}`); rr.Code != 200 {
		t.Fatal("own admin pause failed")
	}
	prefs, err := db.GetCrawlPreferences(t.Context(), uid)
	if err != nil || prefs.Paused {
		t.Fatal("admin changed another account's pause")
	}
	rr := postJSON(t, h, "/api/queue/jobs", admin, `{}`)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), urlA) {
		t.Fatal("admin saw another private job")
	}
	for _, action := range []string{"cancel", "retry"} {
		if rr := postJSON(t, h, "/api/queue/action", admin, `{"url":"`+urlA+`","action":"`+action+`"}`); rr.Code != 404 {
			t.Fatal("admin changed another account's job", rr.Code)
		}
	}
	if rr := postJSON(t, h, "/api/queue/action", token, `{"url":"`+urlA+`","action":"cancel"}`); rr.Code != 200 || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("own cancellation failed", rr.Code)
	}
	if u, _, _, err := db.GetNextPendingNode(); err != nil || u != "" {
		t.Fatal("cancelled node claimed")
	}
	if rr := do(t, h, "GET", "/api/nodes?status=cancelled", token); rr.Code != 200 {
		t.Fatal("cancelled crawl filter rejected", rr.Code)
	}
	if rr := postJSON(t, h, "/api/saved-searches", token, `{"name":"Cancelled jobs","status":"cancelled"}`); rr.Code != 201 {
		t.Fatal("cancelled saved criterion rejected", rr.Code)
	}
	when := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if rr := postJSON(t, h, "/api/queue/action", token, `{"url":"`+urlA+`","action":"schedule","scheduled_at":"`+when+`"}`); rr.Code != 200 {
		t.Fatal("scheduling failed", rr.Code)
	}
	rr = postJSON(t, h, "/api/queue/jobs", token, `{"status":"pending"}`)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), when) {
		t.Fatal("schedule not returned")
	}
	for _, body := range []string{`{"url":"` + urlA + `","action":"delete"}`, `{"url":"` + urlA + `","action":"cancel","scheduled_at":"bad"}`, `{"url":"` + urlA + `","action":"schedule","scheduled_at":"tomorrow"}`, `{"status":"unknown"}`} {
		path := "/api/queue/action"
		if strings.Contains(body, `"status"`) {
			path = "/api/queue/jobs"
		}
		if rr := postJSON(t, h, path, token, body); rr.Code != 400 {
			t.Fatal("invalid queue operation accepted", rr.Code)
		}
	}
	if rr := do(t, h, "GET", "/api/queue", admin); rr.Code != 200 || strings.Contains(rr.Body.String(), urlA) {
		t.Fatal("admin queue summary escaped caller scope")
	}
}
func TestManualRecrawlAndJobActionsRespectSubmissionQuota(t *testing.T) {
	h, db := newAPI(t, func(c *Config) { c.Engine.MaxPendingPerUser = 1 })
	uid, token := mkUser(t, db, "quota@example.com", "user")
	if _, err := db.SaveNode(urlA, "Saved", "", 200, "completed", "{}", "text", "wiki", uid); err != nil {
		t.Fatal(err)
	}
	if err := db.EnqueueURL(urlB, 0, uid, 1); err != nil {
		t.Fatal(err)
	}
	if rr := postJSON(t, h, "/api/crawl/bulk", token, `{"urls":["`+urlA+`extra"]}`); rr.Code != 429 || !strings.Contains(rr.Body.String(), `"error"`) || !strings.Contains(rr.Body.String(), `"items"`) {
		t.Fatal("quota response lost its legacy error or per-item results", rr.Code)
	}
	for _, request := range []struct{ path, body string }{{"/api/recrawl", `{"url":"` + urlA + `"}`}, {"/api/queue/action", `{"url":"` + urlA + `","action":"retry"}`}} {
		if rr := postJSON(t, h, request.path, token, request.body); rr.Code != 429 {
			t.Fatal("retry quota bypass", rr.Code)
		}
	}
	if _, err := db.Conn.Exec(`UPDATE nodes SET processing_status='crawling' WHERE user_id=$1 AND url=$2`, uid, urlA); err != nil {
		t.Fatal(err)
	}
	if rr := postJSON(t, h, "/api/recrawl", token, `{"url":"`+urlA+`"}`); rr.Code != 409 {
		t.Fatal("inflight recrawl overwritten", rr.Code)
	}
	if rr := postJSON(t, h, "/api/queue/action", token, `{"url":"`+urlA+`","action":"cancel"}`); rr.Code != 409 {
		t.Fatal("inflight cancellation accepted", rr.Code)
	}
}
func TestBatchPreviewIsReadOnlyAndChargesTwentyURLsOnce(t *testing.T) {
	h, db := newAPI(t)
	uid, token := mkUser(t, db, "batch@example.com", "user")
	urls := make([]string, 20)
	for i := range urls {
		urls[i] = fmt.Sprintf("%sfixture-%d", urlA, i)
	}
	body, _ := json.Marshal(map[string]any{"urls": urls})
	rr := postJSON(t, h, "/api/crawl/bulk/preview", token, string(body))
	if rr.Code != 200 {
		t.Fatal("preview failed", rr.Code)
	}
	if n, _ := db.CountPendingNodes(uid); n != 0 {
		t.Fatal("preview wrote to queue")
	}
	rr = postJSON(t, h, "/api/crawl/bulk", token, string(body))
	if rr.Code != 202 {
		t.Fatal("20 URLs charged as 21", rr.Code)
	}
	if n, _ := db.CountPendingNodes(uid); n != 20 {
		t.Fatal("accepted batch missing entries")
	}
	if rr := postJSON(t, h, "/api/crawl", token, `{"url":"`+urlB+`"}`); rr.Code != 429 {
		t.Fatal("batch did not consume URL rate budget")
	}
}
func TestQueueMetadataAndPauseIncludedInPrivateExport(t *testing.T) {
	h, db := newAPI(t)
	uid, token := mkUser(t, db, "export@example.com", "admin")
	other, _ := mkUser(t, db, "other@example.com", "user")
	for _, id := range []int{uid, other} {
		if _, err := db.Conn.Exec(`INSERT INTO nodes(url,host,user_id,processing_status,retry_count,last_failure_code) SELECT $1||i,$2,$3,'failed',3,'network' FROM generate_series(1,205) i`, urlA, strings.Split(strings.TrimPrefix(urlA, "http://"), "/")[0], id); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetCrawlPaused(t.Context(), uid, true); err != nil {
		t.Fatal(err)
	}
	rr := do(t, h, "GET", "/api/privacy/export", token)
	var exported struct {
		Version     int                       `json:"format_version"`
		Preferences database.CrawlPreferences `json:"crawl_preferences"`
		Jobs        []database.CrawlJob       `json:"crawl_jobs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &exported); err != nil || rr.Code != 200 || exported.Version != 5 || !exported.Preferences.Paused || len(exported.Jobs) != 205 {
		t.Fatal("private export incomplete", rr.Code, err)
	}
	for _, job := range exported.Jobs {
		if job.FailureCode != "network" || job.RetryCount != 3 {
			t.Fatal("job metadata missing")
		}
	}
}
