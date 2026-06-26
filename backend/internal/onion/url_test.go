package onion

import "testing"

const validHost = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaad.onion"

func TestIsV3Hostname(t *testing.T) {
	tests := []struct {
		name string
		host string
		want bool
	}{
		{"valid", validHost, true},
		{"uppercase", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD.ONION", true},
		{"short", "short.onion", false},
		{"clearnet_suffix", validHost + ".evil.com", false},
		{"bad_base32_char", "11111111111111111111111111111111111111111111111111111111.onion", false},
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
}
