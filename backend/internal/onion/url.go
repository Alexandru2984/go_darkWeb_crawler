package onion

import (
	"bytes"
	"encoding/base32"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/sha3"
)

var v3HostnameRE = regexp.MustCompile(`^[a-z2-7]{56}\.onion$`)

const (
	v3AddressBytes = 35
	v3Version      = byte(3)
)

var onionBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// IsV3Hostname reports whether hostname is a cryptographically valid Tor v3
// onion hostname. Length/base32 checks alone are insufficient: the address
// encodes an Ed25519 public key, a two-byte SHA3 checksum and version 3.
func IsV3Hostname(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if !v3HostnameRE.MatchString(hostname) {
		return false
	}
	encoded := strings.TrimSuffix(hostname, ".onion")
	decoded, err := onionBase32.DecodeString(strings.ToUpper(encoded))
	if err != nil || len(decoded) != v3AddressBytes || decoded[34] != v3Version {
		return false
	}
	payload := make([]byte, 0, len(".onion checksum")+32+1)
	payload = append(payload, ".onion checksum"...)
	payload = append(payload, decoded[:32]...)
	payload = append(payload, v3Version)
	checksum := sha3.Sum256(payload)
	return bytes.Equal(decoded[32:34], checksum[:2])
}

// NormalizeHostname validates an onion host (optionally with a web port) and
// returns the canonical hostname without a port. Blacklisting is domain-wide,
// so callers must not treat :80 and :443 as separate security principals.
func NormalizeHostname(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	hostname := host
	if strings.Contains(host, ":") {
		var port string
		var err error
		hostname, port, err = net.SplitHostPort(host)
		if err != nil || !validWebPort(port) {
			return ""
		}
	}
	if !IsV3Hostname(hostname) {
		return ""
	}
	return hostname
}

// IsV3Host reports whether host is a v3 onion host, optionally on HTTP(S)
// virtual ports 80 or 443. Restricting ports prevents the crawler from being
// repurposed as a general-purpose authenticated port scanner.
func IsV3Host(host string) bool {
	return NormalizeHostname(host) != ""
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
	if parsed.User != nil || parsed.Opaque != "" || parsed.Host == "" {
		return false
	}
	return IsV3Host(parsed.Host)
}

// NormalizeURL canonicalizes and validates a Tor web URL. It strips fragments
// (which are never sent over HTTP), rejects embedded userinfo, adds the root
// path when omitted, and preserves the path/query exactly.
func NormalizeURL(rawURL string) string {
	if rawURL == "" || len(rawURL) > 2048 {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return ""
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	parsed.RawFragment = ""
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	if !IsV3URL(parsed.String()) {
		return ""
	}
	return parsed.String()
}

func validWebPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && (n == 80 || n == 443)
}
