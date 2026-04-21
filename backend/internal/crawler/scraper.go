package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

// ScrapeResult retine datele curatate din pagina onion
type ScrapeResult struct {
	Title        string
	Content      string
	FoundOnions  []string
	ServerHeader string
	StatusCode   int
	Metadata     string // JSON string
	Category     string // ex: "marketplace", "forum", "wiki", "unknown"
}

var spaceRegex = regexp.MustCompile(`\s+`)

// ScrapePage descarca si parseaza o pagina HTML returnand titlul si linkurile onion gasite.
// Accepta un context pentru a putea fi anulat la shutdown.
func ScrapePage(ctx context.Context, client *http.Client, targetURL string) (*ScrapeResult, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("eroare la creare request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; rv:109.0) Gecko/20100101 Firefox/115.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eroare la executia cererii: %w", err)
	}
	defer resp.Body.Close()

	result := &ScrapeResult{
		StatusCode:   resp.StatusCode,
		ServerHeader: truncateUTF8(resp.Header.Get("Server"), 100),
		FoundOnions:  []string{},
		Metadata:     "{}",
		Category:     "unknown",
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "" && !strings.Contains(contentType, "text/html") &&
		!strings.Contains(contentType, "text/plain") && !strings.Contains(contentType, "xml") {
		return result, nil
	}

	// Protectie OOM: citim maxim 1MB (suficient pentru orice pagina utila)
	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return result, nil
	}

	result.Title = truncateUTF8(strings.TrimSpace(doc.Find("title").Text()), 512)

	doc.Find("script, style, noscript, iframe").Remove()
	content := strings.TrimSpace(spaceRegex.ReplaceAllString(doc.Find("body").Text(), " "))
	const maxContentBytes = 100 * 1024 // 100KB — previne stocarea paginilor gigantice in DB
	result.Content = truncateUTF8(content, maxContentBytes)

	metaDataMap := make(map[string]string)
	doc.Find("meta").Each(func(i int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		content, _ := s.Attr("content")
		if name == "description" || name == "keywords" {
			metaDataMap[name] = truncateUTF8(strings.TrimSpace(content), 1000)
		}
	})
	if len(metaDataMap) > 0 {
		if jsonBytes, err := json.Marshal(metaDataMap); err == nil {
			result.Metadata = string(jsonBytes)
		}
	}

	// Rezolvam URL-urile relative fata de pagina curenta si pastram path-ul complet
	baseURL, _ := url.Parse(targetURL)

	// collectOnion rezolva un href/src fata de baseURL, normalizeaza si il adauga daca e .onion
	collectOnion := func(href string) {
		if href == "" || href == "#" || strings.HasPrefix(href, "javascript:") ||
			strings.HasPrefix(href, "mailto:") {
			return
		}
		parsed, err := url.Parse(strings.TrimSpace(href))
		if err != nil {
			return
		}
		resolved := baseURL.ResolveReference(parsed)
		resolved.Fragment = "" // fragmentele (#section) nu schimba continutul paginii
		resolved.Scheme = strings.ToLower(resolved.Scheme)
		resolved.Host = strings.ToLower(resolved.Host)
		if resolved.Path == "" {
			resolved.Path = "/"
		}
		if (resolved.Scheme == "http" || resolved.Scheme == "https") &&
			strings.HasSuffix(resolved.Host, ".onion") {
			result.FoundOnions = append(result.FoundOnions, resolved.String())
		}
	}

	// <a href>
	doc.Find("a[href]").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		collectOnion(href)
	})

	// <area href> (image maps)
	doc.Find("area[href]").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		collectOnion(href)
	})

	// <link rel="canonical"> si alte <link href>
	doc.Find("link[href]").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		collectOnion(href)
	})

	// <form action>
	doc.Find("form[action]").Each(func(i int, s *goquery.Selection) {
		action, _ := s.Attr("action")
		collectOnion(action)
	})

	// <meta http-equiv="refresh" content="5; url=...">
	doc.Find(`meta[http-equiv="refresh"], meta[http-equiv="Refresh"]`).Each(func(i int, s *goquery.Selection) {
		content, _ := s.Attr("content")
		// Format: "delay; url=..." sau "delay; URL=..."
		lower := strings.ToLower(content)
		if idx := strings.Index(lower, "url="); idx != -1 {
			collectOnion(strings.TrimSpace(content[idx+4:]))
		}
	})
	result.FoundOnions = removeDuplicates(result.FoundOnions)
	// Limitam numarul de linkuri per pagina pentru a preveni flood-ul in coada
	const maxFoundOnions = 300
	if len(result.FoundOnions) > maxFoundOnions {
		result.FoundOnions = result.FoundOnions[:maxFoundOnions]
	}
	result.Category = Categorize(result.Title, result.Content)

	return result, nil
}

func removeDuplicates(elements []string) []string {
	seen := make(map[string]struct{}, len(elements))
	result := make([]string, 0, len(elements))
	for _, v := range elements {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			result = append(result, v)
		}
	}
	return result
}

// truncateUTF8 taie un string la maxBytes octeti fara sa rupa secvente UTF-8 multi-byte.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	b := s[:maxBytes]
	for len(b) > 0 && !utf8.RuneStart(b[len(b)-1]) {
		b = b[:len(b)-1]
	}
	return b
}
