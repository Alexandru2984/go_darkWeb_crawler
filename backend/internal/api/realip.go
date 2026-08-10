package api

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// networkHeader is set by nginx on every proxied request and says which of the
// two front doors the request came through. Both vhosts set it explicitly, so a
// client cannot pick its own value: whatever it sends is overwritten.
const networkHeader = "X-Client-Network"

// Which front door a request arrived through. This is not cosmetic: the onion
// vhost has no client address to report, so every Tor visitor reaches the API
// as 127.0.0.1 and per-IP accounting silently becomes per-everyone accounting.
const (
	networkClearnet = "clearnet"
	networkOnion    = "onion"
)

// ClientNetwork reports the front door a request came through, as decided by
// TrustedRealIP. Requests that never passed that middleware, and requests that
// reached the API without going through a trusted proxy, count as clearnet.
func ClientNetwork(r *http.Request) string {
	if n, ok := r.Context().Value(networkContextKey).(string); ok {
		return n
	}
	return networkClearnet
}

// TrustedRealIP applies proxy-provided client IP headers only when the direct
// peer is a local/private reverse proxy. This preserves nginx/Cloudflare real
// IP handling while preventing a directly exposed API from trusting spoofed
// X-Real-IP or X-Forwarded-For headers.
//
// It also records which vhost the request came through, and must do so here:
// the client-IP rewrite below destroys the evidence. After the rewrite
// RemoteAddr holds the *client* address, so a later trust check would be
// asking about the wrong host entirely.
func TrustedRealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remote := remoteIP(r.RemoteAddr)
		network := networkClearnet
		if isTrustedProxyIP(remote) {
			if strings.EqualFold(strings.TrimSpace(r.Header.Get(networkHeader)), networkOnion) {
				network = networkOnion
			}
			if ip := firstValidClientIP(r.Header.Get("X-Real-IP")); ip != "" {
				r.RemoteAddr = net.JoinHostPort(ip, "0")
			} else if ip := firstValidClientIP(r.Header.Get("X-Forwarded-For")); ip != "" {
				r.RemoteAddr = net.JoinHostPort(ip, "0")
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), networkContextKey, network)))
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
