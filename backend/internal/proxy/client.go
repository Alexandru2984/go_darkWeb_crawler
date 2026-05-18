package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// NewTorClient creates an HTTP client that routes exclusively through SOCKS5 (Tor)
func NewTorClient(socksProxyAddress string) (*http.Client, error) {
	_, client, err := NewTorClientWithTransport(socksProxyAddress)
	return client, err
}

// NewTorClientWithTransport returneaza atat transport-ul cat si clientul,
// astfel incat engine-ul sa poata apela CloseIdleConnections() dupa SIGNAL NEWNYM.
func NewTorClientWithTransport(socksProxyAddress string) (*http.Transport, *http.Client, error) {
	dialer, err := proxy.SOCKS5("tcp", socksProxyAddress, nil, proxy.Direct)
	if err != nil {
		return nil, nil, fmt.Errorf("error initializing SOCKS5: %w", err)
	}

	// Use DialContext if the SOCKS5 dialer supports it (cancellable context).
	// Altfel, wrap cu verificare context inainte de dial.
	type contextDialer interface {
		DialContext(ctx context.Context, network, address string) (net.Conn, error)
	}
	dialCtx := func(ctx context.Context, network, address string) (net.Conn, error) {
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
			if !strings.HasSuffix(req.URL.Hostname(), ".onion") {
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
