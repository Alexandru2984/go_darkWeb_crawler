package onion

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var v3HostnameRE = regexp.MustCompile(`^[a-z2-7]{56}\.onion$`)

// IsV3Hostname reports whether hostname is exactly a Tor v3 onion hostname.
func IsV3Hostname(hostname string) bool {
	return v3HostnameRE.MatchString(strings.ToLower(strings.TrimSpace(hostname)))
}

// IsV3Host reports whether host is a v3 onion host, optionally with a valid port.
func IsV3Host(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if strings.Contains(host, ":") {
		hostname, port, err := net.SplitHostPort(host)
		if err != nil || !validPort(port) {
			return false
		}
		return IsV3Hostname(hostname)
	}
	return IsV3Hostname(host)
}

// IsV3URL reports whether rawURL is an http(s) URL on a Tor v3 onion host.
func IsV3URL(rawURL string) bool {
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
	return IsV3Host(parsed.Host)
}

// NormalizeURL canonicalizes only the scheme and host. Path/query are preserved.
func NormalizeURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String()
}

func validPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}
