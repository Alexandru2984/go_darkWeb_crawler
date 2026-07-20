package proxy

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"onion-spider/internal/onion"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// isolationCredential maps a destination host to a stable, opaque SOCKS5
// credential. Tor treats a differing username/password as a separate isolation
// domain, so one credential per host yields one circuit per host.
//
// The value is hashed rather than passing the hostname through verbatim: SOCKS5
// caps credentials at 255 bytes, and a fixed-width token keeps the field free of
// anything the remote site could influence. Truncating to 128 bits is ample —
// this only needs to avoid collisions between hostnames, not resist preimages.
func isolationCredential(host string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(host)))
	return hex.EncodeToString(sum[:16])
}

// NewTorClient creates an HTTP client that routes exclusively through SOCKS5 (Tor)
func NewTorClient(socksProxyAddress string) (*http.Client, error) {
	_, client, err := NewTorClientWithTransport(socksProxyAddress)
	return client, err
}

// NewTorClientWithTransport returneaza atat transport-ul cat si clientul,
// astfel incat engine-ul sa poata apela CloseIdleConnections() dupa SIGNAL NEWNYM.
func NewTorClientWithTransport(socksProxyAddress string) (*http.Transport, *http.Client, error) {
	// Validate the proxy address once, up front. The real dialers are built
	// per-destination below, so without this an unusable address would only
	// surface on the first crawl instead of at client construction.
	if _, err := proxy.SOCKS5("tcp", socksProxyAddress, nil, proxy.Direct); err != nil {
		return nil, nil, fmt.Errorf("error initializing SOCKS5: %w", err)
	}

	type contextDialer interface {
		DialContext(ctx context.Context, network, address string) (net.Conn, error)
	}

	// STREAM ISOLATION: a fresh SOCKS5 dialer per destination host, each with a
	// distinct username/password derived from that host. Tor enables
	// IsolateSOCKSAuth on SocksPort by default, so distinct credentials force
	// distinct circuits.
	//
	// Without this, every request a worker makes shares one isolation key and
	// Tor is free to carry crawls of unrelated onion services over the same
	// circuit — which hands any relay on that circuit a correlation: "the same
	// client is reading site A and site B". Keying on the host (rather than
	// per-request) keeps one circuit per site, so connection reuse and the
	// per-domain politeness delay still behave as before.
	dialCtx := func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		cred := isolationCredential(host)
		dialer, err := proxy.SOCKS5("tcp", socksProxyAddress, &proxy.Auth{User: cred, Password: cred}, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("error initializing SOCKS5: %w", err)
		}
		if cd, ok := dialer.(contextDialer); ok {
			return cd.DialContext(ctx, network, address)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return dialer.Dial(network, address)
	}

	// InsecureSkipVerify is INTENTIONAL for .onion crawling: there is no PKI
	// rooted in a .onion namespace, so almost every onion service ships a
	// self-signed certificate. The certificate's identity guarantees are
	// already provided by Tor's NTOR handshake plus the v3 onion address
	// (which is the public key), so x509 verification adds nothing here and
	// would simply reject every site. MinVersion still enforces TLS 1.2+.
	//
	// CodeQL flags this as "Disabled TLS certificate check"; the alert is
	// expected for an onion crawler and should be dismissed as won't-fix
	// with a reference to this comment.
	//nolint:gosec // G402: see comment above — onion services don't have a CA chain.
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
	}

	transport := &http.Transport{
		DialContext:           dialCtx,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			// Only allow redirects within the .onion space — clearnet is forbidden
			if !onion.IsV3Hostname(req.URL.Hostname()) {
				return fmt.Errorf("redirect to clearnet blocked: %s", req.URL.Host)
			}
			// Do not follow redirects to another onion domain (prevents cross-site tracking)
			if req.URL.Hostname() != via[0].URL.Hostname() {
				return fmt.Errorf("redirect to another onion domain blocked: %s -> %s", via[0].URL.Host, req.URL.Host)
			}
			return nil
		},
	}

	return transport, client, nil
}
