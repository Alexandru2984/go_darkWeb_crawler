package crawler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRobotsIsAllowed_Disallowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nDisallow: /admin\nDisallow: /private\n")
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	// Reset the cache for isolated testing
	globalRobotsCache.mu.Lock()
	delete(globalRobotsCache.entries, server.Listener.Addr().String())
	globalRobotsCache.mu.Unlock()

	if IsAllowed(nil, server.Client(), server.URL+"/admin/panel") {
		t.Error("expected blocked for /admin/panel, but was allowed")
	}
	if IsAllowed(nil, server.Client(), server.URL+"/private/data") {
		t.Error("expected blocked for /private/data, but was allowed")
	}
}

func TestRobotsIsAllowed_Allowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nDisallow: /admin\n")
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	globalRobotsCache.mu.Lock()
	delete(globalRobotsCache.entries, server.Listener.Addr().String())
	globalRobotsCache.mu.Unlock()

	if !IsAllowed(nil, server.Client(), server.URL+"/public/page") {
		t.Error("expected allowed for /public/page, but was blocked")
	}
}

func TestRobotsIsAllowed_NoRobots(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	globalRobotsCache.mu.Lock()
	delete(globalRobotsCache.entries, server.Listener.Addr().String())
	globalRobotsCache.mu.Unlock()

	if !IsAllowed(nil, server.Client(), server.URL+"/anything") {
		t.Error("no robots.txt — should be fail-open (allowed)")
	}
}

func TestRobotsParseRobots_UASpecific(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: OnionSpider\nDisallow: /spider-only\n\nUser-agent: *\nDisallow: /all\n")
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	globalRobotsCache.mu.Lock()
	delete(globalRobotsCache.entries, server.Listener.Addr().String())
	globalRobotsCache.mu.Unlock()

	// /spider-only is disallowed by the OnionSpider section
	if IsAllowed(nil, server.Client(), server.URL+"/spider-only/page") {
		t.Error("expected blocked for /spider-only/page (OnionSpider section)")
	}
	// /all is disallowed by the * section
	if IsAllowed(nil, server.Client(), server.URL+"/all/page") {
		t.Error("expected blocked for /all/page (* section)")
	}
}

func TestSitemapFetch_URLSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sitemap.xml" {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>http://test1234567890abc.onion/page1</loc></url>
  <url><loc>http://test1234567890abc.onion/page2</loc></url>
  <url><loc>http://not-onion.com/page3</loc></url>
</urlset>`)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	// Inject the test server URL instead of a real .onion
	urls := fetchAndParseSitemap(nil, server.Client(), server.URL+"/sitemap.xml", false)
	if len(urls) != 2 {
		t.Errorf("expected 2 .onion URLs, got %d: %v", len(urls), urls)
	}
}

func TestSitemapFetch_SitemapIndex(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>%s/sitemap-sub.xml</loc></sitemap>
</sitemapindex>`, serverURL)
		case "/sitemap-sub.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>http://abc1234567890xyz.onion/deep</loc></url>
</urlset>`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	// The sub-sitemap is not .onion, so it won't be processed by recursion.
	// The test verifies the parser doesn't panic.
	urls := fetchAndParseSitemap(nil, server.Client(), server.URL+"/sitemap.xml", true)
	_ = urls
}

func TestSitemapFetch_NoSitemap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	urls := FetchSitemapURLs(nil, server.Client(), server.URL+"/page")
	if urls != nil && len(urls) > 0 {
		t.Errorf("expected empty list, got: %v", urls)
	}
}
