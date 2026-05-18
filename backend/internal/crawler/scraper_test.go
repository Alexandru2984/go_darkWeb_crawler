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
	// Create a test HTTP server that emulates an onion site
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
					// The crawler should not extract this text
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
		t.Fatalf("Unexpected error running scraper: %v", err)
	}

	// 1. Check the Title
	if result.Title != "The Hidden Test Wiki" {
		t.Errorf("Incorrect title. Expected 'The Hidden Test Wiki', got '%s'", result.Title)
	}

	// 2. Check Server Header
	if result.ServerHeader != "TestServer/1.0" {
		t.Errorf("Incorrect Server Header. Expected 'TestServer/1.0', got '%s'", result.ServerHeader)
	}

	// 3. Check Content Extraction (clean text)
	expectedContent := "Welcome to the Hidden Wiki This is a paragraph with important content. DuckDuckGo Local Page (Should be ignored)"
	if result.Content != expectedContent {
		t.Errorf("Incorrect extracted content.\nExpected: '%s'\nGot: '%s'", expectedContent, result.Content)
	}

	// 4. Check Onion Link Extraction (with full path)
	if len(result.FoundOnions) != 1 {
		t.Fatalf("Expected 1 onion link, got %d: %v", len(result.FoundOnions), result.FoundOnions)
	}

	// The full URL with path must be preserved (fix #5)
	expectedOnion := "http://duckduckgogg42xjoc72x3sjiqbvzwsgxgjvpeqg5unfxgf2fsvawd.onion/search?q=test"
	if result.FoundOnions[0] != expectedOnion {
		t.Errorf("Incorrect extracted onion link. Expected '%s', got '%s'", expectedOnion, result.FoundOnions[0])
	}
}

func TestScrapePageExtraLinkSources(t *testing.T) {
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
		t.Fatalf("Unexpected error: %v", err)
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
			t.Errorf("Expected URL was not extracted: %s\nFound: %v", want, result.FoundOnions)
		}
	}
}

func TestScrapePageContentTruncation(t *testing.T) {
	// Page with content larger than the 100KB limit
	bigContent := strings.Repeat("a", 200*1024)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>Big Page</title></head><body>%s</body></html>`, bigContent)
	}))
	defer server.Close()

	result, err := ScrapePage(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	const maxBytes = 100 * 1024
	if len(result.Content) > maxBytes {
		t.Errorf("Content not truncated: %d bytes, max %d bytes", len(result.Content), maxBytes)
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
		t.Fatalf("Unexpected error: %v", err)
	}
	if result.Content != "" {
		t.Errorf("Content extracted from a binary file — expected empty, got: %q", result.Content)
	}
}
