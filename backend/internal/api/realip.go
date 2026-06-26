package api

import (
	"net"
	"net/http"
	"strings"
)

// TrustedRealIP applies proxy-provided client IP headers only when the direct
// peer is a local/private reverse proxy. This preserves nginx/Cloudflare real
// IP handling while preventing a directly exposed API from trusting spoofed
// X-Real-IP or X-Forwarded-For headers.
func TrustedRealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remote := remoteIP(r.RemoteAddr)
		if isTrustedProxyIP(remote) {
			if ip := firstValidClientIP(r.Header.Get("X-Real-IP")); ip != "" {
				r.RemoteAddr = net.JoinHostPort(ip, "0")
			} else if ip := firstValidClientIP(r.Header.Get("X-Forwarded-For")); ip != "" {
				r.RemoteAddr = net.JoinHostPort(ip, "0")
			}
		}
		next.ServeHTTP(w, r)
	})
}

func remoteIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return net.ParseIP(strings.TrimSpace(host))
}

func isTrustedProxyIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

func firstValidClientIP(header string) string {
	for _, part := range strings.Split(header, ",") {
		ip := net.ParseIP(strings.TrimSpace(part))
		if ip != nil {
			return ip.String()
		}
	}
	return ""
}
