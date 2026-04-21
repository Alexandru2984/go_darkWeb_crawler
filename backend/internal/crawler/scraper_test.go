package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScrapePage(t *testing.T) {
	// Cream un server HTTP de test care emuleaza un site onion
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Server", "TestServer/1.0")
		w.WriteHeader(200)
		w.Write([]byte(`
			<html>
			<head>
				<title>The Hidden Test Wiki</title>
				<meta name="description" content="A test page for the crawler">
				<meta name="keywords" content="test, crawler, spider">
			</head>
			<body>
				<h1>Welcome to the Hidden Wiki</h1>
				<p>This is a paragraph with <strong>important content</strong>.</p>
				
				<script>
					// Crawlerul nu trebuie sa extraga acest text
					console.log("Secret hacker code");
				</script>
				
				<style>
					body { color: black; }
				</style>
				
				<ul>
					<li><a href="http://duckduckgogg42xjoc72x3sjiqbvzwsgxgjvpeqg5unfxgf2fsvawd.onion/search?q=test">DuckDuckGo</a></li>
					<li><a href="/local-page">Local Page (Should be ignored)</a></li>
				</ul>
			</body>
			</html>
		`))
	}))
	defer server.Close()

	client := server.Client()

	result, err := ScrapePage(context.Background(), client, server.URL)
	if err != nil {
		t.Fatalf("Eroare neasteptata la rularea scraper-ului: %v", err)
	}

	// 1. Verificam Titlul
	if result.Title != "The Hidden Test Wiki" {
		t.Errorf("Titlu incorect. Asteptat 'The Hidden Test Wiki', primit '%s'", result.Title)
	}

	// 2. Verificam Server Header
	if result.ServerHeader != "TestServer/1.0" {
		t.Errorf("Server Header incorect. Asteptat 'TestServer/1.0', primit '%s'", result.ServerHeader)
	}

	// 3. Verificam Extragerea Continutului (Text curat)
	expectedContent := "Welcome to the Hidden Wiki This is a paragraph with important content. DuckDuckGo Local Page (Should be ignored)"
	if result.Content != expectedContent {
		t.Errorf("Content extras incorect.\nAsteptat: '%s'\nPrimit: '%s'", expectedContent, result.Content)
	}

	// 4. Verificam Extragerea Link-urilor Onion (cu path complet)
	if len(result.FoundOnions) != 1 {
		t.Fatalf("Asteptam 1 link onion, am primit %d: %v", len(result.FoundOnions), result.FoundOnions)
	}

	// URL-ul complet cu path trebuie pastrat (fix #5)
	expectedOnion := "http://duckduckgogg42xjoc72x3sjiqbvzwsgxgjvpeqg5unfxgf2fsvawd.onion/search?q=test"
	if result.FoundOnions[0] != expectedOnion {
		t.Errorf("Link onion extras incorect. Asteptat '%s', primit '%s'", expectedOnion, result.FoundOnions[0])
	}
}

func TestScrapePageExtraLinkSources(t *testing.T) {
	const onionURL = "http://test1234567890abc.onion/page"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head>
			<link rel="canonical" href="http://canonical1234567890.onion/canon">
			<meta http-equiv="refresh" content="5; url=http://refresh1234567890.onion/redir">
		</head><body>
			<form action="http://form1234567890abcde.onion/submit">
				<input type="submit">
			</form>
			<map><area href="http://area1234567890abcd.onion/area"></map>
		</body></html>`)
	}))
	defer server.Close()

	result, err := ScrapePage(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("Eroare neasteptata: %v", err)
	}

	wantURLs := []string{
		"http://canonical1234567890.onion/canon",
		"http://refresh1234567890.onion/redir",
		"http://form1234567890abcde.onion/submit",
		"http://area1234567890abcd.onion/area",
	}

	found := make(map[string]bool)
	for _, u := range result.FoundOnions {
		found[u] = true
	}
	for _, want := range wantURLs {
		if !found[want] {
			t.Errorf("URL-ul asteptat nu a fost extras: %s\nGasit: %v", want, result.FoundOnions)
		}
	}
}

func TestScrapePageContentTruncation(t *testing.T) {
	// Pagina cu continut mai mare decat limita de 100KB
	bigContent := strings.Repeat("a", 200*1024)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>Big Page</title></head><body>%s</body></html>`, bigContent)
	}))
	defer server.Close()

	result, err := ScrapePage(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("Eroare neasteptata: %v", err)
	}

	const maxBytes = 100 * 1024
	if len(result.Content) > maxBytes {
		t.Errorf("Continut netruncat: %d bytes, max %d bytes", len(result.Content), maxBytes)
	}
}

func TestScrapePageNonHTMLSkipped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte{0x00, 0x01, 0x02, 0x03})
	}))
	defer server.Close()

	result, err := ScrapePage(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("Eroare neasteptata: %v", err)
	}
	if result.Content != "" {
		t.Errorf("Continut extras dintr-un fisier binar — asteptat gol, primit: %q", result.Content)
	}
}
