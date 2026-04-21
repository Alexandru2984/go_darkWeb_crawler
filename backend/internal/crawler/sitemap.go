package crawler

import (
	"context"
	"encoding/xml"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// sitemapIndex reprezinta un fisier sitemap care listeaza alte fisiere sitemap
type sitemapIndex struct {
	XMLName  xml.Name      `xml:"sitemapindex"`
	Sitemaps []sitemapLoc  `xml:"sitemap"`
}

type sitemapLoc struct {
	Loc string `xml:"loc"`
}

// urlSet reprezinta un fisier sitemap standard cu URL-uri de pagini
type urlSet struct {
	XMLName xml.Name   `xml:"urlset"`
	URLs    []urlEntry `xml:"url"`
}

type urlEntry struct {
	Loc string `xml:"loc"`
}

// FetchSitemapURLs descarca /sitemap.xml de la un host onion si returneaza URL-urile .onion gasite.
// Suporta sitemapindex (un nivel de recursie) + urlset standard.
// Fail-open: returneaza lista goala daca nu exista sitemap sau eroare.
func FetchSitemapURLs(ctx context.Context, client *http.Client, siteURL string) []string {
	parsed, err := url.Parse(siteURL)
	if err != nil {
		return nil
	}

	sitemapURL := parsed.Scheme + "://" + parsed.Host + "/sitemap.xml"
	return fetchAndParseSitemap(ctx, client, sitemapURL, true)
}

// fetchAndParseSitemap descarca un URL de sitemap si extrage URL-urile onion.
// recurse=true permite un singur nivel de recursie pentru sitemapindex.
func fetchAndParseSitemap(ctx context.Context, client *http.Client, sitemapURL string, recurse bool) []string {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", sitemapURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", robotsUA)

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024)) // max 512KB
	if err != nil {
		return nil
	}

	var result []string

	// Incearcam mai intai sitemapindex
	var idx sitemapIndex
	if err := xml.Unmarshal(body, &idx); err == nil && len(idx.Sitemaps) > 0 {
		if recurse {
			for _, sm := range idx.Sitemaps {
				if isOnionURL(sm.Loc) {
					sub := fetchAndParseSitemap(ctx, client, sm.Loc, false) // fara recursie suplimentara
					result = append(result, sub...)
				}
			}
		}
		return result
	}

	// Incercam urlset standard
	var us urlSet
	if err := xml.Unmarshal(body, &us); err != nil {
		log.Printf("[Sitemap] Nu am putut parsa %s: %v", sitemapURL, err)
		return nil
	}

	for _, u := range us.URLs {
		if isOnionURL(u.Loc) {
			result = append(result, u.Loc)
		}
	}
	return result
}

// isOnionURL verifica daca un URL apartine unui domeniu .onion
func isOnionURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && strings.HasSuffix(u.Host, ".onion")
}
