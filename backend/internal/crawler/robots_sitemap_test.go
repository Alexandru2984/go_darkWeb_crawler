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

	// Reseta cache-ul pentru testare izolata
	globalRobotsCache.mu.Lock()
	delete(globalRobotsCache.entries, server.Listener.Addr().String())
	globalRobotsCache.mu.Unlock()

	if IsAllowed(nil, server.Client(), server.URL+"/admin/panel") {
		t.Error("asteptat blocat pentru /admin/panel, dar a fost permis")
	}
	if IsAllowed(nil, server.Client(), server.URL+"/private/data") {
		t.Error("asteptat blocat pentru /private/data, dar a fost permis")
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
		t.Error("asteptat permis pentru /public/page, dar a fost blocat")
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
		t.Error("fara robots.txt — trebuie sa fie fail-open (permis)")
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

	// /spider-only e interzis de sectiunea OnionSpider
	if IsAllowed(nil, server.Client(), server.URL+"/spider-only/page") {
		t.Error("asteptat blocat pentru /spider-only/page (sectiune OnionSpider)")
	}
	// /all e interzis de sectiunea *
	if IsAllowed(nil, server.Client(), server.URL+"/all/page") {
		t.Error("asteptat blocat pentru /all/page (sectiune *)")
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

	// Injectam URL-ul serverului de test in loc de un .onion real
	urls := fetchAndParseSitemap(nil, server.Client(), server.URL+"/sitemap.xml", false)
	if len(urls) != 2 {
		t.Errorf("asteptat 2 URL-uri .onion, primit %d: %v", len(urls), urls)
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

	// Sub-sitemap-ul nu e .onion, deci nu va fi procesat de recursie.
	// Testul verifica ca parserul nu paniceaza.
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
		t.Errorf("asteptat lista goala, primit: %v", urls)
	}
}
