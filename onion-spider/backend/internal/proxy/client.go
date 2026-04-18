package proxy

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

// NewTorClient creeaza un client HTTP care ruteaza exclusiv prin SOCKS5 (Tor)
func NewTorClient(socksProxyAddress string) (*http.Client, error) {
	// Configurarea proxy-ului SOCKS5
	dialer, err := proxy.SOCKS5("tcp", socksProxyAddress, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("eroare la initializarea SOCKS5: %w", err)
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
			// Limitare redirect-uri pentru a nu pica in loops infinite
			if len(via) >= 3 {
				return fmt.Errorf("prea multe redirect-uri")
			}
			return nil
		},
	}

	return client, nil
}
