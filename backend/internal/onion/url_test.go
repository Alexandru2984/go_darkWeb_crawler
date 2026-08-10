package onion

import (
	"strings"
	"testing"
)

// Example address published in Tor's v3 onion-address specification.
const validHost = "pg6mmjiyjmcrsslvykfwnntlaru7p5svn6y2ymmju6nubxndf4pscryd.onion"

func TestIsV3Hostname(t *testing.T) {
	tests := []struct {
		name string
		host string
		want bool
	}{
		{"valid", validHost, true},
		{"uppercase", strings.ToUpper(validHost), true},
		{"short", "short.onion", false},
		{"clearnet_suffix", validHost + ".evil.com", false},
		{"bad_base32_char", "11111111111111111111111111111111111111111111111111111111.onion", false},
		{"valid_shape_bad_checksum", "ag6mmjiyjmcrsslvykfwnntlaru7p5svn6y2ymmju6nubxndf4pscryd.onion", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsV3Hostname(tt.host); got != tt.want {
				t.Fatalf("IsV3Hostname(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestIsV3HostWithPort(t *testing.T) {
	if !IsV3Host(validHost + ":80") {
		t.Fatal("valid v3 onion host with port should pass")
	}
	if !IsV3Host(validHost + ":443") {
		t.Fatal("valid v3 onion host with TLS port should pass")
	}
	if IsV3Host(validHost + ":8080") {
		t.Fatal("non-web port should fail")
	}
	if IsV3Host(validHost + ":99999") {
		t.Fatal("invalid port should fail")
	}
	if IsV3Host(validHost + ":abc") {
		t.Fatal("non-numeric port should fail")
	}
}

func TestIsV3URL(t *testing.T) {
	if !IsV3URL("https://" + validHost + "/path?q=1") {
		t.Fatal("valid v3 onion URL should pass")
	}
	if IsV3URL("ftp://" + validHost) {
		t.Fatal("non-http scheme should fail")
	}
	if IsV3URL("https://" + validHost + ".evil.com") {
		t.Fatal("suffix attack should fail")
	}
	if IsV3URL("http://user:secret@" + validHost + "/") {
		t.Fatal("userinfo credentials must not be accepted")
	}
}

func TestNormalizeURL(t *testing.T) {
	got := NormalizeURL(" HTTPS://" + strings.ToUpper(validHost) + "/Path?q=1#private-fragment ")
	want := "https://" + validHost + "/Path?q=1"
	if got != want {
		t.Fatalf("NormalizeURL = %q, want %q", got, want)
	}
	if got := NormalizeURL("http://user:secret@" + validHost + "/"); got != "" {
		t.Fatalf("NormalizeURL accepted userinfo: %q", got)
	}
	if got := NormalizeHostname(strings.ToUpper(validHost) + ":443"); got != validHost {
		t.Fatalf("NormalizeHostname = %q, want %q", got, validHost)
	}
}
