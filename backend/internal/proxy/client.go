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

// NewTorClient creeaza un client HTTP care ruteaza exclusiv prin SOCKS5 (Tor)
func NewTorClient(socksProxyAddress string) (*http.Client, error) {
	_, client, err := NewTorClientWithTransport(socksProxyAddress)
	return client, err
}

// NewTorClientWithTransport returneaza atat transport-ul cat si clientul,
// astfel incat engine-ul sa poata apela CloseIdleConnections() dupa SIGNAL NEWNYM.
func NewTorClientWithTransport(socksProxyAddress string) (*http.Transport, *http.Client, error) {
	dialer, err := proxy.SOCKS5("tcp", socksProxyAddress, nil, proxy.Direct)
	if err != nil {
		return nil, nil, fmt.Errorf("eroare la initializarea SOCKS5: %w", err)
	}

	// Folosim DialContext daca dialer-ul SOCKS5 il suporta (context cancelabil).
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

	transport := &http.Transport{
		DialContext:           dialCtx,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, // Multe site-uri onion au certificate self-signed
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("prea multe redirect-uri")
			}
			// Permitem redirect-uri numai in spatiul .onion — clearnet-ul e interzis
			if !strings.HasSuffix(req.URL.Hostname(), ".onion") {
				return fmt.Errorf("redirect catre clearnet blocat: %s", req.URL.Host)
			}
			// Nu urmarim redirect-uri catre alt domeniu onion (previne tracking cross-site)
			if req.URL.Hostname() != via[0].URL.Hostname() {
				return fmt.Errorf("redirect catre alt domeniu onion blocat: %s -> %s", via[0].URL.Host, req.URL.Host)
			}
			return nil
		},
	}

	return transport, client, nil
}
