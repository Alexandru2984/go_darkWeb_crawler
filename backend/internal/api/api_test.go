package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ── CrawlLimiter ──────────────────────────────────────────────────────────────

func TestCrawlLimiter_AllowsUnderLimit(t *testing.T) {
	lim := NewCrawlLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !lim.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestCrawlLimiter_BlocksAtLimit(t *testing.T) {
	lim := NewCrawlLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		lim.Allow("1.2.3.4")
	}
	if lim.Allow("1.2.3.4") {
		t.Fatal("4th request should be blocked")
	}
}

func TestCrawlLimiter_IsolatesIPs(t *testing.T) {
	lim := NewCrawlLimiter(1, time.Minute)
	if !lim.Allow("1.1.1.1") {
		t.Fatal("first IP should be allowed")
	}
	if lim.Allow("1.1.1.1") {
		t.Fatal("first IP second request should be blocked")
	}
	if !lim.Allow("2.2.2.2") {
		t.Fatal("second IP should be allowed independently")
	}
}

func TestCrawlLimiter_WindowReset(t *testing.T) {
	lim := NewCrawlLimiter(1, 10*time.Millisecond)
	if !lim.Allow("10.0.0.1") {
		t.Fatal("first request should be allowed")
	}
	if lim.Allow("10.0.0.1") {
		t.Fatal("second request before window expires should be blocked")
	}
	time.Sleep(20 * time.Millisecond)
	if !lim.Allow("10.0.0.1") {
		t.Fatal("request after window reset should be allowed")
	}
}

func TestCrawlLimiter_MaxBucketsRejectsNew(t *testing.T) {
	lim := &CrawlLimiter{
		buckets:    make(map[string]*limitBucket),
		limit:      5,
		window:     time.Hour,
		maxBuckets: 2,
	}
	lim.Allow("192.168.0.1")
	lim.Allow("192.168.0.2")
	// buckets full; third distinct IP should be rejected
	if lim.Allow("192.168.0.3") {
		t.Fatal("new IP should be rejected when bucket map is full")
	}
}

// ── IsValidOnionURL ───────────────────────────────────────────────────────────

func TestIsValidOnionURL_ValidV3(t *testing.T) {
	valid := []string{
		"http://facebookwkhpilnemxj7asber7cyc673hlcrbjnoa7iwmqrxyqqipcid.onion",
		"https://facebookwkhpilnemxj7asber7cyc673hlcrbjnoa7iwmqrxyqqipcid.onion",
		"http://facebookwkhpilnemxj7asber7cyc673hlcrbjnoa7iwmqrxyqqipcid.onion/path?q=1",
	}
	for _, u := range valid {
		if !IsValidOnionURL(u) {
			t.Errorf("expected valid: %s", u)
		}
	}
}

func TestIsValidOnionURL_Invalid(t *testing.T) {
	invalid := []string{
		"",
		"http://google.com",
		"ftp://facebookwkhpilnemxj7asber7cyc673hlcrbjnoa7iwmqrxyqqipcid.onion",
		"http://short.onion",
		"javascript:alert(1)",
		"http://facebookwkhpilnemxj7asber7cyc673hlcrbjnoa7iwmqrxyqqipcid.onion.evil.com",
	}
	for _, u := range invalid {
		if IsValidOnionURL(u) {
			t.Errorf("expected invalid: %s", u)
		}
	}
}

func TestIsValidOnionURL_TooLong(t *testing.T) {
	long := "http://" + string(make([]byte, 2048)) + ".onion"
	if IsValidOnionURL(long) {
		t.Fatal("URL exceeding 2048 chars should be invalid")
	}
}

// ── SanitizeCSVField ──────────────────────────────────────────────────────────

func TestSanitizeCSVField_SafeStrings(t *testing.T) {
	safe := []string{"hello", "world", "123", "normal text"}
	for _, s := range safe {
		if got := SanitizeCSVField(s); got != s {
			t.Errorf("SanitizeCSVField(%q) = %q, want unchanged", s, got)
		}
	}
}

func TestSanitizeCSVField_FormulaInjection(t *testing.T) {
	injections := []struct {
		input string
		want  string
	}{
		{"=SUM(A1:A10)", "'=SUM(A1:A10)"},
		{"+cmd|' /C calc'!A0", "'+cmd|' /C calc'!A0"},
		{"-1+1", "'-1+1"},
		{"@SUM(1+1)", "'@SUM(1+1)"},
		{"\t=injection", "'\t=injection"},
		{"\r=injection", "'\r=injection"},
	}
	for _, tc := range injections {
		got := SanitizeCSVField(tc.input)
		if got != tc.want {
			t.Errorf("SanitizeCSVField(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSanitizeCSVField_Empty(t *testing.T) {
	if got := SanitizeCSVField(""); got != "" {
		t.Errorf("SanitizeCSVField(\"\") = %q, want \"\"", got)
	}
}

// ── ClientIP ─────────────────────────────────────────────────────────────────

func TestClientIP_FromRemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.1:54321"
	if got := ClientIP(r); got != "192.0.2.1" {
		t.Errorf("ClientIP = %q, want %q", got, "192.0.2.1")
	}
}

func TestClientIP_IPv6(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[::1]:54321"
	if got := ClientIP(r); got != "::1" {
		t.Errorf("ClientIP = %q, want %q", got, "::1")
	}
}

func TestClientIP_NoPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1"
	// SplitHostPort will fail, should return RemoteAddr as-is
	if got := ClientIP(r); got != "10.0.0.1" {
		t.Errorf("ClientIP = %q, want %q", got, "10.0.0.1")
	}
}

// ── ParsePagination ───────────────────────────────────────────────────────────

func TestParsePagination_Defaults(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	limit, offset := ParsePagination(r)
	if limit != 50 {
		t.Errorf("default limit = %d, want 50", limit)
	}
	if offset != 0 {
		t.Errorf("default offset = %d, want 0", offset)
	}
}

func TestParsePagination_ValidValues(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?limit=20&page=3", nil)
	limit, offset := ParsePagination(r)
	if limit != 20 {
		t.Errorf("limit = %d, want 20", limit)
	}
	if offset != 40 { // (3-1)*20
		t.Errorf("offset = %d, want 40", offset)
	}
}

func TestParsePagination_OverLimit(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?limit=999", nil)
	limit, _ := ParsePagination(r)
	if limit != 50 {
		t.Errorf("oversized limit should default to 50, got %d", limit)
	}
}

func TestParsePagination_PageCap(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?page=99999", nil)
	limit, offset := ParsePagination(r)
	// page capped at 10000
	expected := (10000 - 1) * limit
	if offset != expected {
		t.Errorf("offset for capped page = %d, want %d", offset, expected)
	}
}

func TestParsePagination_NegativeAndZero(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?limit=-5&page=-1", nil)
	limit, offset := ParsePagination(r)
	if limit != 50 {
		t.Errorf("negative limit should default to 50, got %d", limit)
	}
	if offset != 0 {
		t.Errorf("negative page should produce offset 0, got %d", offset)
	}
}

// ── ValidatePassword ──────────────────────────────────────────────────────────

func TestValidatePassword_TooShort(t *testing.T) {
	if err := ValidatePassword("Aa1!Bb"); err == nil {
		t.Fatal("password under 10 chars should be rejected")
	}
}

func TestValidatePassword_TooLong(t *testing.T) {
	long := make([]byte, 73)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidatePassword(string(long)); err == nil {
		t.Fatal("password over 72 chars should be rejected")
	}
}

func TestValidatePassword_OneCategory(t *testing.T) {
	if err := ValidatePassword("aaaaaaaaaaaa"); err == nil {
		t.Fatal("single-category password should be rejected")
	}
}

func TestValidatePassword_ThreeCategories(t *testing.T) {
	if err := ValidatePassword("Abcdefg1234"); err != nil {
		t.Errorf("3-category password should pass, got: %v", err)
	}
}

// ── NormalizeOnionURL ─────────────────────────────────────────────────────────

func TestNormalizeOnionURL_LowercasesSchemeAndHost(t *testing.T) {
	in := "HTTP://FacebookWkhpilnemxj7asber7cyc673hlcrbjnoa7iwmqrxyqqipcid.ONION/Path"
	want := "http://facebookwkhpilnemxj7asber7cyc673hlcrbjnoa7iwmqrxyqqipcid.onion/Path"
	if got := NormalizeOnionURL(in); got != want {
		t.Errorf("NormalizeOnionURL = %q, want %q", got, want)
	}
}

func TestNormalizeOnionURL_EmptyHost(t *testing.T) {
	if got := NormalizeOnionURL("not a url"); got != "" {
		t.Errorf("invalid input should return empty, got %q", got)
	}
}
