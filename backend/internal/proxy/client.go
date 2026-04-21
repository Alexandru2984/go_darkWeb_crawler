package proxy

import (
	"crypto/tls"
	"fmt"
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
	// Configurarea proxy-ului SOCKS5
	dialer, err := proxy.SOCKS5("tcp", socksProxyAddress, nil, proxy.Direct)
	if err != nil {
		return nil, nil, fmt.Errorf("eroare la initializarea SOCKS5: %w", err)
	}

	// Crearea unui transport HTTP custom, legat de dialer-ul SOCKS5
	transport := &http.Transport{
		Dial:                  dialer.Dial,
		DialContext:           nil, // Important pentru a forta DNS resolution prin Tor
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, // Multe site-uri onion au certificate self-signed
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second, // Daca site-ul nu raspunde, renuntam
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
