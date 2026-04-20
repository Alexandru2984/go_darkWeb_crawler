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
		ServerHeader: resp.Header.Get("Server"),
		FoundOnions:  []string{},
		Metadata:     "{}",
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "" && !strings.Contains(contentType, "text/html") &&
		!strings.Contains(contentType, "text/plain") && !strings.Contains(contentType, "xml") {
		return result, nil
	}

	// Protectie OOM: citim maxim 5MB
	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return result, nil
	}

	result.Title = strings.TrimSpace(doc.Find("title").Text())

	doc.Find("script, style, noscript, iframe").Remove()
	content := strings.TrimSpace(spaceRegex.ReplaceAllString(doc.Find("body").Text(), " "))
	const maxContentBytes = 100 * 1024 // 100KB — previne stocarea paginilor gigantice in DB
	if len(content) > maxContentBytes {
		content = content[:maxContentBytes]
	}
	result.Content = content

	metaDataMap := make(map[string]string)
	doc.Find("meta").Each(func(i int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		content, _ := s.Attr("content")
		if name == "description" || name == "keywords" {
			metaDataMap[name] = strings.TrimSpace(content)
		}
	})
	if len(metaDataMap) > 0 {
		if jsonBytes, err := json.Marshal(metaDataMap); err == nil {
			result.Metadata = string(jsonBytes)
		}
	}

	// Rezolvam URL-urile relative fata de pagina curenta si pastram path-ul complet
	baseURL, _ := url.Parse(targetURL)
	doc.Find("a[href]").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		if href == "" || href == "#" || strings.HasPrefix(href, "javascript:") {
			return
		}
		parsed, err := url.Parse(href)
		if err != nil {
			return
		}
		resolved := baseURL.ResolveReference(parsed)
		resolved.Fragment = "" // fragmentele (#section) nu schimba continutul paginii
		if (resolved.Scheme == "http" || resolved.Scheme == "https") &&
			strings.HasSuffix(resolved.Host, ".onion") {
			result.FoundOnions = append(result.FoundOnions, resolved.String())
		}
	})
	result.FoundOnions = removeDuplicates(result.FoundOnions)

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
