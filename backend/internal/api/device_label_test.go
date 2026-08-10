package api

import "testing"

func TestDeviceLabelPrefersTheMoreSpecificBrowser(t *testing.T) {
	// Browsers impersonate each other, so the order of the checks is the whole
	// correctness story: Edge claims Chrome and Safari, Chrome claims Safari.
	cases := []struct{ ua, want string }{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36 Edg/120.0", "Edge on Windows"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36", "Chrome on Windows"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15", "Safari on macOS"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:115.0) Gecko/20100101 Firefox/115.0", "Firefox on Linux"},
		{"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Mobile Safari/537.36", "Chrome on Android"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1", "Safari on iOS"},
		{"curl/8.5.0", "curl"},
		{"", "Unknown device"},
		{"something entirely unrecognisable", "Unknown device"},
	}
	for _, c := range cases {
		if got := DeviceLabel(c.ua); got != c.want {
			t.Errorf("DeviceLabel(%.40q) = %q, want %q", c.ua, got, c.want)
		}
	}
}

func TestDeviceLabelDiscardsTheIdentifyingDetail(t *testing.T) {
	// The label is stored for as long as the session row lives, so it must not
	// carry versions, build IDs or locale — the parts that make a User-Agent a
	// near-unique fingerprint.
	ua := "Mozilla/5.0 (X11; Linux x86_64; rv:115.0esr; en-GB) Gecko/20100101 Firefox/115.0.2esr Build/2024010112"
	label := DeviceLabel(ua)
	for _, leaked := range []string{"115", "20100101", "en-GB", "esr", "x86_64", "Build"} {
		if contains(label, leaked) {
			t.Errorf("label %q leaked %q from the User-Agent", label, leaked)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func TestDeviceLabelRejectsAbsurdlyLongInput(t *testing.T) {
	long := make([]byte, 4096)
	for i := range long {
		long[i] = 'a'
	}
	if got := DeviceLabel("Firefox/" + string(long)); got != "Unknown device" {
		t.Errorf("oversized User-Agent produced %q", got)
	}
}
