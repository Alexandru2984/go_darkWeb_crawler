package api

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// EmailRegex is a basic email format check — not full RFC 5322, just enough
// to filter obviously invalid input before sending to SMTP/DB.
var EmailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// v3OnionHostRE accepts only valid v3 .onion addresses: 56 base32 chars (a-z2-7)
// + ".onion", optionally with a port. v2 (16 chars) has been deprecated by Tor.
var v3OnionHostRE = regexp.MustCompile(`^[a-z2-7]{56}\.onion(:[0-9]{1,5})?$`)

// TokenSafeRE validates email-verify / password-reset tokens: hex, base64url
// or alphanumeric + '-_'. Removes any HTML/URL injection risk when the token
// is reflected in a page.
var TokenSafeRE = regexp.MustCompile(`^[A-Za-z0-9_\-]{16,128}$`)

// IsValidOnionURL returns true iff rawURL is an http(s) URL whose host is a v3 onion address.
func IsValidOnionURL(rawURL string) bool {
	if rawURL == "" || len(rawURL) > 2048 {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Host)
	return v3OnionHostRE.MatchString(host)
}

// NormalizeOnionURL produces a canonical form for an onion URL: scheme + host
// lowercased, path/query preserved. Returns "" if the URL is not parseable.
func NormalizeOnionURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String()
}

// ValidatePassword enforces 10..72 chars (72 = bcrypt limit) and at least 3
// of {lowercase, uppercase, digit, symbol}. Blocks trivial passwords like
// "passwordaa" or "aaaaaaaaaa".
func ValidatePassword(p string) error {
	if len(p) < 10 || len(p) > 72 {
		return errors.New("password must be between 10 and 72 characters")
	}
	var hasLower, hasUpper, hasDigit, hasSymbol bool
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= '0' && r <= '9':
			hasDigit = true
		case r > 32 && r < 127:
			hasSymbol = true
		}
	}
	classes := 0
	for _, ok := range []bool{hasLower, hasUpper, hasDigit, hasSymbol} {
		if ok {
			classes++
		}
	}
	if classes < 3 {
		return errors.New("password must combine at least 3 categories: lowercase, uppercase, digits, symbols")
	}
	return nil
}
