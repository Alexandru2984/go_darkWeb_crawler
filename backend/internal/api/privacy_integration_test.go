package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"onion-spider/internal/database"
)

const privacyPassword = "correct-horse-battery-staple-9"

func postJSON(t *testing.T, h http.Handler, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doWithBody(t, h, "POST", target, token, strings.NewReader(body))
}

// A live session is not enough to destroy data. Whoever steals a session cookie
// gets to act as the user for its whole lifetime, and "delete everything" is
// exactly what they would reach for — so every destructive endpoint here has to
// ask for the password again.
func TestDestructivePrivacyActionsRequireThePasswordAgain(t *testing.T) {
	h, db := newAPI(t)
	uid, token := mkVerifiedUser(t, db, "purge@example.com", privacyPassword, "user")

	if _, err := db.SaveNode(urlA, "t", "nginx", 200, "completed", "", "body", "market", uid); err != nil {
		t.Fatalf("SaveNode: %v", err)
	}

	for _, c := range []struct{ name, target, body string }{
		{"delete crawl history", "/api/privacy/purge", `{"scope":"crawl_history","password":"wrong-password"}`},
		{"delete page content", "/api/privacy/purge", `{"scope":"page_content","password":"wrong-password"}`},
		{"delete the account", "/api/privacy/account/delete", `{"password":"wrong-password"}`},
	} {
		rr := postJSON(t, h, c.target, token, c.body)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s with a wrong password returned %d, want 401", c.name, rr.Code)
		}
	}

	// Nothing was destroyed by the refused attempts.
	var nodes int
	if err := db.Conn.QueryRow(`SELECT COUNT(*) FROM nodes WHERE user_id = $1`, uid).Scan(&nodes); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if nodes != 1 {
		t.Errorf("%d nodes remain after refused deletions, want 1", nodes)
	}
	settings, err := db.GetPrivacySettings(uid)
	if err != nil {
		t.Fatalf("GetPrivacySettings: %v", err)
	}
	if settings.DeletionScheduledFor != nil {
		t.Error("a refused request still scheduled the account for deletion")
	}

	// With the real password the same call goes through.
	rr := postJSON(t, h, "/api/privacy/purge", token,
		`{"scope":"crawl_history","password":"`+privacyPassword+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("purge with the correct password returned %d: %s", rr.Code, rr.Body.String())
	}
	if err := db.Conn.QueryRow(`SELECT COUNT(*) FROM nodes WHERE user_id = $1`, uid).Scan(&nodes); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if nodes != 0 {
		t.Errorf("%d nodes remain after an authorised purge, want 0", nodes)
	}
}

// Deleting an account is scheduled, never immediate. The delay is the only
// thing that lets the real owner reverse a deletion someone else triggered.
func TestAccountDeletionIsScheduledAndReversible(t *testing.T) {
	h, db := newAPI(t, func(c *Config) { c.DeletionGrace = 48 * time.Hour })
	uid, token := mkVerifiedUser(t, db, "leaving@example.com", privacyPassword, "user")

	rr := postJSON(t, h, "/api/privacy/account/delete", token,
		`{"password":"`+privacyPassword+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete request returned %d: %s", rr.Code, rr.Body.String())
	}

	// The account still exists and still works — only the timer was set.
	settings, err := db.GetPrivacySettings(uid)
	if err != nil {
		t.Fatalf("GetPrivacySettings: %v", err)
	}
	if settings.DeletionScheduledFor == nil {
		t.Fatal("no deletion was scheduled")
	}
	if until := time.Until(*settings.DeletionScheduledFor); until < 47*time.Hour {
		t.Errorf("deletion is due in %v, want roughly the configured 48h grace", until)
	}
	due, err := db.DueAccountDeletions(10)
	if err != nil {
		t.Fatalf("DueAccountDeletions: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("account is already due for deletion during its grace period: %+v", due)
	}

	// The warning follows the user around, not just on the page they'd have to
	// think to visit.
	rr = do(t, h, "GET", "/api/auth/me", token)
	var me map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode /api/auth/me: %v", err)
	}
	if me["deletion_scheduled_for"] == nil {
		t.Error("/api/auth/me does not report the pending deletion")
	}

	// Cancelling needs no password: it only ever preserves data, and a prompt
	// in front of the undo button is what turns a misclick into a loss.
	rr = postJSON(t, h, "/api/privacy/account/delete/cancel", token, `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("cancel returned %d: %s", rr.Code, rr.Body.String())
	}
	settings, err = db.GetPrivacySettings(uid)
	if err != nil {
		t.Fatalf("GetPrivacySettings after cancel: %v", err)
	}
	if settings.DeletionScheduledFor != nil {
		t.Error("deletion is still scheduled after cancelling")
	}
}

// A second factor is part of the credential. An account that enrolled one must
// not have its data deleted by someone holding only the password.
func TestDeletionRequiresTheSecondFactorWhenEnrolled(t *testing.T) {
	h, db := newAPI(t)
	_, token := mkVerifiedUser(t, db, "mfa-delete@example.com", privacyPassword, "user")
	secret, recovery := enrolTOTP(t, h, token)

	rr := postJSON(t, h, "/api/privacy/account/delete", token,
		`{"password":"`+privacyPassword+`"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("deletion without a second factor returned %d, want 401", rr.Code)
	}
	rr = postJSON(t, h, "/api/privacy/account/delete", token,
		`{"password":"`+privacyPassword+`","code":"000000"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("deletion with a wrong code returned %d, want 401", rr.Code)
	}

	// A recovery code is a legitimate second factor here, as it is at login.
	rr = postJSON(t, h, "/api/privacy/account/delete", token,
		`{"password":"`+privacyPassword+`","code":"`+recovery[0]+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("deletion with a recovery code returned %d: %s", rr.Code, rr.Body.String())
	}
	_ = secret
}

// The personal-data export is the account's own record, not an administrative
// dump. An admin exporting their data must not receive every other tenant's
// crawl records inside it.
func TestPersonalExportIsScopedToTheAccountEvenForAdmins(t *testing.T) {
	h, db := newAPI(t)
	adminID, adminToken := mkVerifiedUser(t, db, "admin-export@example.com", privacyPassword, "admin")
	otherID, _ := mkVerifiedUser(t, db, "other-export@example.com", privacyPassword, "user")

	if _, err := db.SaveNode(urlA, "mine", "nginx", 200, "completed", "", "body", "market", adminID); err != nil {
		t.Fatalf("SaveNode(admin): %v", err)
	}
	if _, err := db.SaveNode(urlB, "theirs", "nginx", 200, "completed", "", "body", "market", otherID); err != nil {
		t.Fatalf("SaveNode(other): %v", err)
	}

	rr := do(t, h, "GET", "/api/privacy/export", adminToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("export returned %d: %s", rr.Code, rr.Body.String())
	}

	var doc struct {
		Account struct {
			Email string `json:"email"`
		} `json:"account"`
		CrawlRecords []database.Node `json:"crawl_records"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("export is not valid JSON: %v\n%s", err, rr.Body.String())
	}
	if doc.Account.Email != "admin-export@example.com" {
		t.Errorf("export names %q as the account", doc.Account.Email)
	}
	// Asserted on the URL rather than the user_id field: ExportNodes does not
	// select user_id, so it is zero on every row and would make this check pass
	// for the wrong reason. urlB belongs to the other account and must be absent.
	for _, n := range doc.CrawlRecords {
		if n.URL == urlB {
			t.Errorf("export contains another account's record: %s", n.URL)
		}
	}
	if len(doc.CrawlRecords) != 1 || doc.CrawlRecords[0].URL != urlA {
		t.Errorf("export carries %d crawl records, want only the account's own", len(doc.CrawlRecords))
	}

	// Credential material must never reach a file that will be downloaded,
	// synced and mailed around.
	body := rr.Body.String()
	for _, forbidden := range []string{"password_hash", "totp_secret", "code_hash", "verification_token", "reset_token"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("export contains credential material: %q", forbidden)
		}
	}
}

// Someone else's account must not be reachable through these endpoints at all,
// and an unauthenticated caller must not learn anything from them.
func TestPrivacyEndpointsRejectUnauthenticatedCallers(t *testing.T) {
	h, _ := newAPI(t)
	for _, c := range []struct{ method, target string }{
		{"GET", "/api/privacy/settings"},
		{"POST", "/api/privacy/settings"},
		{"GET", "/api/privacy/export"},
		{"POST", "/api/privacy/purge"},
		{"POST", "/api/privacy/account/delete"},
		{"POST", "/api/privacy/account/delete/cancel"},
	} {
		rr := do(t, h, c.method, c.target, "")
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s unauthenticated returned %d, want 401", c.method, c.target, rr.Code)
		}
	}
}

// The metadata-only switch has to actually reach the crawler's write path.
func TestMetadataOnlySettingStopsContentBeingStored(t *testing.T) {
	h, db := newAPI(t)
	uid, token := mkVerifiedUser(t, db, "metadata@example.com", privacyPassword, "user")

	rr := postJSON(t, h, "/api/privacy/settings", token, `{"retention_days":0,"store_content":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("settings update returned %d: %s", rr.Code, rr.Body.String())
	}

	if _, err := db.SaveNode(urlA, "a title", "nginx", 200, "completed", "", "sensitive page body", "market", uid); err != nil {
		t.Fatalf("SaveNode: %v", err)
	}
	var content, title string
	if err := db.Conn.QueryRow(
		`SELECT COALESCE(content, ''), COALESCE(title, '') FROM nodes WHERE url = $1 AND user_id = $2`,
		urlA, uid).Scan(&content, &title); err != nil {
		t.Fatalf("read node: %v", err)
	}
	if content != "" {
		t.Errorf("page text was stored for a metadata-only account: %q", content)
	}
	if title != "a title" {
		t.Errorf("metadata was dropped along with the content: title=%q", title)
	}
}

// A retention window the schema would refuse must be rejected at the edge with
// a message, not surfaced as a 500.
func TestRetentionSettingIsValidatedAtTheEdge(t *testing.T) {
	h, db := newAPI(t)
	_, token := mkVerifiedUser(t, db, "range-api@example.com", privacyPassword, "user")

	rr := postJSON(t, h, "/api/privacy/settings", token, `{"retention_days":-5,"store_content":true}`)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("negative retention returned %d, want 400", rr.Code)
	}
	rr = postJSON(t, h, "/api/privacy/settings", token, `{"retention_days":99999,"store_content":true}`)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("absurd retention returned %d, want 400", rr.Code)
	}
}
