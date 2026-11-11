package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"onion-spider/internal/database"
)

func TestNodeFiltersAndPagesRemainScoped(t *testing.T) {
	h, db := newAPI(t)
	const password = "Correct-Horse-9!"
	uid, token := mkVerifiedUser(t, db, "filters@example.com", password, "user")
	other, _ := mkVerifiedUser(t, db, "filters-other@example.com", password, "user")
	const host = "http://pg6mmjiyjmcrsslvykfwnntlaru7p5svn6y2ymmju6nubxndf4pscryd.onion/"

	for i := range 5 {
		category, status := "news", "completed"
		if i == 3 {
			category = "forum"
		}
		if i == 4 {
			status = "failed"
		}
		if _, err := db.SaveNode(fmt.Sprintf("%s%d", host, i), "Research report",
			"nginx", 200, status, "{}", "Research report", category, uid); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SaveNode(host+"private", "Research report private", "nginx",
		200, "completed", "{}", "Research report", "news", other); err != nil {
		t.Fatal(err)
	}

	var pages [][]database.Node
	for page := 1; page <= 2; page++ {
		rr := do(t, h, http.MethodGet,
			fmt.Sprintf("/api/nodes?category=news&status=completed&limit=2&page=%d", page), token)
		if rr.Code != http.StatusOK {
			t.Fatalf("page %d: %d %s", page, rr.Code, rr.Body)
		}
		if more := rr.Header().Get("X-Has-More") == "true"; more != (page == 1) {
			t.Fatalf("page %d: incorrect continuation %v", page, more)
		}
		var nodes []database.Node
		if err := json.Unmarshal(rr.Body.Bytes(), &nodes); err != nil {
			t.Fatal(err)
		}
		pages = append(pages, nodes)
	}
	if len(pages[0]) != 2 || len(pages[1]) != 1 {
		t.Fatalf("filtered pages contain %d and %d rows", len(pages[0]), len(pages[1]))
	}
	seen := map[int]bool{}
	for _, page := range pages {
		for _, node := range page {
			if seen[node.ID] || node.Category != "news" || node.ProcessingStatus != "completed" {
				t.Fatalf("incorrect filtered result: %+v", node)
			}
			seen[node.ID] = true
			if strings.Contains(node.URL, "private") {
				t.Fatal("another tenant's node leaked")
			}
		}
	}

	rr := postJSON(t, h, "/api/search?limit=2", token,
		`{"q":"Research","category":"news","status":"completed"}`)
	if rr.Code != http.StatusOK || rr.Header().Get("X-Has-More") != "true" || strings.Contains(rr.Body.String(), "private") {
		t.Fatalf("filtered search: %d %s", rr.Code, rr.Body)
	}
	rr = postJSON(t, h, "/api/search?limit=2&page=2", token,
		`{"q":"Research","category":"news","status":"completed"}`)
	var last []database.Node
	if err := json.Unmarshal(rr.Body.Bytes(), &last); err != nil || rr.Code != http.StatusOK || len(last) != 1 || rr.Header().Get("X-Has-More") != "" {
		t.Fatalf("search final page: %d %s, err=%v", rr.Code, rr.Body, err)
	}

	if rr := do(t, h, http.MethodGet, "/api/nodes?status=invalid", token); rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid filter returned %d", rr.Code)
	}
}
