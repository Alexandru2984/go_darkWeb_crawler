package crawler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// ScrapeResult retine datele curatate din pagina onion
type ScrapeResult struct {
	Title        string
	FoundOnions  []string
	ServerHeader string
	StatusCode   int
	Metadata     string // JSON string
}

// ScrapePage descarca si parseaza o pagina HTML returnand titlul si linkurile onion gasite
func ScrapePage(client *http.Client, targetURL string) (*ScrapeResult, error) {
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("eroare la creare request: %w", err)
	}
	
	// Ne deghizam intr-un Tor Browser uzual pentru a evita anumite firewalls
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
		Title:        "",
		Metadata:     "{}",
	}

	// 1. Verificam Content-Type (daca e imagine sau binar masiv, il sarim la parsare)
	contentType := resp.Header.Get("Content-Type")
	if contentType != "" && !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "text/plain") && !strings.Contains(contentType, "xml") {
		return result, nil // Ne oprim aici, returnam doar status code-ul, nu parsam body-ul
	}

	// 2. Protectie OOM: Citim maxim 5MB (5 * 1024 * 1024 bytes)
	limitReader := io.LimitReader(resp.Body, 5*1024*1024)

	// Incarcam HTML-ul in goquery limitat
	doc, err := goquery.NewDocumentFromReader(limitReader)
	if err == nil {
		result.Title = strings.TrimSpace(doc.Find("title").Text())

		// Extragem meta description & keywords
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

		// Extragem toate tag-urile <a href="...">
		doc.Find("a").Each(func(i int, s *goquery.Selection) {
			href, exists := s.Attr("href")
			if exists {
				// Incercam sa parsăm URL-ul pentru a curata calea
				parsedUrl, err := url.Parse(href)
				if err == nil {
					host := parsedUrl.Host
					if host != "" && strings.HasSuffix(host, ".onion") {
						fullOnion := fmt.Sprintf("%s://%s", parsedUrl.Scheme, host)
						result.FoundOnions = append(result.FoundOnions, fullOnion)
					}
				}
			}
		})
		result.FoundOnions = removeDuplicates(result.FoundOnions)
	}

	return result, nil
}

func removeDuplicates(elements []string) []string {
	encountered := map[string]bool{}
	result := []string{}

	for v := range elements {
		if !encountered[elements[v]] {
			encountered[elements[v]] = true
			result = append(result, elements[v])
		}
	}
	return result
}
