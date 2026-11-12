package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"onion-spider/internal/onion"
)

var ErrResearchFilters = errors.New("invalid research filters: use a valid onion hostname, supported sort, title up to 200 characters and a valid discovery-date range")

// These fields are private research preferences, carried only in JSON bodies.
type ResearchFilters struct {
	Title string `json:"title"`
	Host  string `json:"host"`
	From  string `json:"discovered_from"`
	To    string `json:"discovered_to"`
	Tag   string `json:"tag"`
	Sort  string `json:"sort"`
}

type BrowseInput struct {
	Query    string `json:"q"`
	Category string `json:"category"`
	Status   string `json:"status"`
	ResearchFilters
}

func NormalizeResearchFilters(input ResearchFilters) (ResearchFilters, error) {
	input.Title = strings.TrimSpace(input.Title)
	if !utf8.ValidString(input.Title) || utf8.RuneCountInString(input.Title) > 200 || strings.IndexFunc(input.Title, unicode.IsControl) >= 0 {
		return input, ErrResearchFilters
	}
	if input.Host != "" {
		if len(input.Host) > 253 {
			return input, ErrResearchFilters
		}
		input.Host = onion.NormalizeHostname(input.Host)
		if input.Host == "" {
			return input, ErrResearchFilters
		}
	}
	if input.Tag != "" {
		var err error
		input.Tag, err = NormalizeCollectionName(input.Tag)
		if err != nil {
			return input, err
		}
	}
	for _, value := range []string{input.From, input.To} {
		if value == "" {
			continue
		}
		date, err := time.Parse("2006-01-02", value)
		if err != nil || date.Year() < 1 || len(value) != 10 {
			return input, ErrResearchFilters
		}
	}
	if input.From != "" && input.To != "" && input.From > input.To {
		return input, ErrResearchFilters
	}
	switch input.Sort {
	case "", "recent", "oldest", "title", "relevance":
	default:
		return input, ErrResearchFilters
	}
	return input, nil
}

// BrowseNodes binds every user value as a parameter. A collection always limits
// results to the caller's own graph, even for a global-data administrator.
func (db *DB) BrowseNodes(ctx context.Context, input BrowseInput, limit, offset, userID int, isAdmin bool) ([]Node, error) {
	input.Query = strings.TrimSpace(input.Query)
	if err := ValidateSearchCriteria(input.Query, input.Category, input.Status); err != nil {
		return nil, err
	}
	filters, err := NormalizeResearchFilters(input.ResearchFilters)
	if err != nil {
		return nil, err
	}
	input.ResearchFilters = filters
	args := []any{userID, isAdmin && input.Tag == ""}
	where := []string{"(n.user_id=$1 OR $2)"}
	bind := func(value any) string { args = append(args, value); return fmt.Sprintf("$%d", len(args)) }
	queryParam := ""
	if input.Query != "" {
		queryParam = bind(input.Query)
		where = append(where, "n.search_vector @@ plainto_tsquery('english',"+queryParam+")")
	}
	if input.Category != "" {
		where = append(where, "COALESCE(n.category,'unknown')="+bind(input.Category))
	}
	if input.Status != "" {
		where = append(where, "n.processing_status="+bind(input.Status))
	}
	if input.Title != "" {
		literal := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(input.Title)
		where = append(where, `n.title ILIKE `+bind("%"+literal+"%")+` ESCAPE '\'`)
	}
	if input.Host != "" {
		where = append(where, "n.host="+bind(input.Host))
	}
	if input.From != "" {
		where = append(where, "n.discovered_at >= "+bind(input.From)+"::date")
	}
	if input.To != "" {
		where = append(where, "n.discovered_at < "+bind(input.To)+"::date + INTERVAL '1 day'")
	}
	if input.Tag != "" {
		where = append(where, "EXISTS(SELECT 1 FROM node_tags t WHERE t.user_id=$1 AND t.node_id=n.id AND t.tag="+bind(input.Tag)+")")
	}
	order := "n.discovered_at DESC NULLS LAST,n.id DESC"
	switch input.Sort {
	case "oldest":
		order = "n.discovered_at ASC NULLS LAST,n.id ASC"
	case "title":
		order = "lower(COALESCE(n.title,'')) ASC,n.id DESC"
	case "relevance", "":
		if queryParam != "" {
			order = "ts_rank(n.search_vector,plainto_tsquery('english'," + queryParam + ")) DESC,n.id DESC"
		}
	}
	limitParam, offsetParam := bind(limit), bind(offset)
	rows, err := db.Conn.QueryContext(ctx, `SELECT n.id,n.url,COALESCE(n.title,''),COALESCE(n.status_code,0),COALESCE(n.server_header,''),
		n.processing_status,COALESCE(n.category,'unknown'),to_char(n.last_crawled_at,'YYYY-MM-DD HH24:MI:SS')
		FROM nodes n WHERE `+strings.Join(where, " AND ")+" ORDER BY "+order+" LIMIT "+limitParam+" OFFSET "+offsetParam, args...)
	if err != nil {
		return nil, err
	}
	return scanNodes(rows)
}
