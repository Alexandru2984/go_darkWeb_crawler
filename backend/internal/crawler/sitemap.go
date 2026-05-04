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

// sitemapIndex represents a sitemap file that lists other sitemap files
type sitemapIndex struct {
	XMLName  xml.Name      `xml:"sitemapindex"`
	Sitemaps []sitemapLoc  `xml:"sitemap"`
}

type sitemapLoc struct {
	Loc string `xml:"loc"`
}

// urlSet represents a standard sitemap file with page URLs
type urlSet struct {
	XMLName xml.Name   `xml:"urlset"`
	URLs    []urlEntry `xml:"url"`
}

type urlEntry struct {
	Loc string `xml:"loc"`
}

// FetchSitemapURLs downloads /sitemap.xml from an onion host and returns the .onion URLs found.
// Supports sitemapindex (one level of recursion) + standard urlset.
// Fail-open: returns an empty list if the sitemap doesn't exist or on error.
func FetchSitemapURLs(ctx context.Context, client *http.Client, siteURL string) []string {
	parsed, err := url.Parse(siteURL)
	if err != nil {
		return nil
	}

	sitemapURL := parsed.Scheme + "://" + parsed.Host + "/sitemap.xml"
	return fetchAndParseSitemap(ctx, client, sitemapURL, true)
}

// fetchAndParseSitemap downloads a sitemap URL and extracts onion URLs.
// recurse=true allows a single level of recursion for sitemapindex.
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

	const (
		maxSitemapURLs  = 300
		maxChildSitemaps = 50
	)

	var result []string

	// Try sitemapindex first
	var idx sitemapIndex
	if err := xml.Unmarshal(body, &idx); err == nil && len(idx.Sitemaps) > 0 {
		if recurse {
			for i, sm := range idx.Sitemaps {
				if i >= maxChildSitemaps {
					break
				}
				if isOnionURL(sm.Loc) {
					sub := fetchAndParseSitemap(ctx, client, sm.Loc, false) // no further recursion
					result = append(result, sub...)
					if len(result) >= maxSitemapURLs {
						result = result[:maxSitemapURLs]
						break
					}
				}
			}
		}
		return result
	}

	// Try standard urlset
	var us urlSet
	if err := xml.Unmarshal(body, &us); err != nil {
		log.Printf("[Sitemap] Could not parse %s: %v", sitemapURL, err)
		return nil
	}

	for _, u := range us.URLs {
		if isOnionURL(u.Loc) {
			result = append(result, u.Loc)
			if len(result) >= maxSitemapURLs {
				break
			}
		}
	}
	return result
}

// isOnionURL checks whether a URL belongs to a .onion domain
func isOnionURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && strings.HasSuffix(u.Host, ".onion")
}
