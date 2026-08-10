package api

import "strings"

// DeviceLabel reduces a User-Agent to a coarse "Browser on OS" family, or
// "Unknown device" when it does not look like a browser.
//
// The raw User-Agent is deliberately never stored. It carries version numbers,
// build identifiers and locale hints that together are close to a unique
// fingerprint, and a session row outlives the session itself. What a user needs
// from this list is only enough to recognise which of their own devices a row
// refers to, so that is all it keeps.
//
// Order matters throughout: several browsers impersonate others in their
// User-Agent, so the more specific claim has to be tested first. Chrome's
// string contains "Safari", Edge's contains both "Chrome" and "Safari", and
// almost everything on Windows contains "Mozilla".
func DeviceLabel(userAgent string) string {
	ua := strings.ToLower(userAgent)
	if ua == "" || len(userAgent) > 2048 {
		return "Unknown device"
	}

	browser := ""
	switch {
	case strings.Contains(ua, "edg/"), strings.Contains(ua, "edga/"), strings.Contains(ua, "edgios/"):
		browser = "Edge"
	case strings.Contains(ua, "opr/"), strings.Contains(ua, "opera"):
		browser = "Opera"
	case strings.Contains(ua, "firefox/"), strings.Contains(ua, "fxios/"):
		// Tor Browser is Firefox ESR and deliberately reports a generic
		// Firefox string. Recording it as anything more specific would defeat
		// the uniformity its users rely on.
		browser = "Firefox"
	case strings.Contains(ua, "chrome/"), strings.Contains(ua, "crios/"):
		browser = "Chrome"
	case strings.Contains(ua, "safari/"):
		browser = "Safari"
	case strings.Contains(ua, "curl/"):
		browser = "curl"
	case strings.Contains(ua, "python"), strings.Contains(ua, "go-http-client"), strings.Contains(ua, "okhttp"):
		browser = "API client"
	}

	os := ""
	switch {
	case strings.Contains(ua, "android"):
		os = "Android"
	case strings.Contains(ua, "iphone"), strings.Contains(ua, "ipad"), strings.Contains(ua, "ipod"):
		os = "iOS"
	case strings.Contains(ua, "windows"):
		os = "Windows"
	case strings.Contains(ua, "mac os x"), strings.Contains(ua, "macintosh"):
		os = "macOS"
	case strings.Contains(ua, "cros"):
		os = "ChromeOS"
	case strings.Contains(ua, "linux"), strings.Contains(ua, "x11"):
		os = "Linux"
	}

	switch {
	case browser != "" && os != "":
		return browser + " on " + os
	case browser != "":
		return browser
	case os != "":
		return os
	default:
		return "Unknown device"
	}
}
