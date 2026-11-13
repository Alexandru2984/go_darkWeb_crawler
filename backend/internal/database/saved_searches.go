package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lib/pq"
)

const (
	MaxSavedSearches   = 50
	MaxSavedSearchName = 80
	MaxSearchQuery     = 200
)

var (
	ErrSearchCriteria       = errors.New("invalid search criteria: use at most 200 query characters, a category of at most 50 bytes and a supported status")
	ErrSavedSearchName      = errors.New("use a search name of 1–80 characters without control characters")
	ErrSavedSearchNotFound  = errors.New("saved search not found")
	ErrSavedSearchDuplicate = errors.New("a saved search with this name already exists")
	ErrSavedSearchLimit     = errors.New("saved search limit reached (maximum 50 per account)")
)

type SavedSearchInput struct {
	ResearchFilters
	Name     string `json:"name"`
	Query    string `json:"q"`
	Category string `json:"category"`
	Status   string `json:"status"`
}

type SavedSearch struct {
	ResearchFilters
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Query     string    `json:"q"`
	Category  string    `json:"category"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func ValidateSearchCriteria(query, category, status string) error {
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) > MaxSearchQuery ||
		strings.ContainsRune(query, 0) || !utf8.ValidString(category) || len(category) > 50 ||
		strings.IndexFunc(category, unicode.IsControl) >= 0 {
		return ErrSearchCriteria
	}
	switch status {
	case "", "pending", "crawling", "completed", "failed", "blocked", "cancelled":
		return nil
	default:
		return ErrSearchCriteria
	}
}

func normalizedSearchName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > MaxSavedSearchName || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", ErrSavedSearchName
	}
	return name, nil
}

func savedSearchWriteError(err error) error {
	var pg *pq.Error
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ErrSavedSearchDuplicate
	}
	return err
}

func scanSavedSearch(row interface{ Scan(...any) error }) (SavedSearch, error) {
	var saved SavedSearch
	err := row.Scan(&saved.ID, &saved.Name, &saved.Query, &saved.Category, &saved.Status, &saved.CreatedAt, &saved.UpdatedAt, &saved.Title, &saved.Host, &saved.From, &saved.To, &saved.Tag, &saved.Sort, &saved.Note, &saved.BookmarkedOnly, &saved.Review)
	return saved, err
}

func (db *DB) ListSavedSearches(ctx context.Context, userID int) ([]SavedSearch, error) {
	rows, err := db.Conn.QueryContext(ctx, `SELECT id, name, query_text, category, status, created_at, updated_at, title_filter, host_filter, discovered_from, discovered_to, collection_tag, result_sort, note_filter, bookmarked_only, review_filter
		FROM saved_searches WHERE user_id=$1 ORDER BY updated_at DESC, id DESC LIMIT $2`, userID, MaxSavedSearches)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	searches := make([]SavedSearch, 0)
	for rows.Next() {
		saved, err := scanSavedSearch(rows)
		if err != nil {
			return nil, err
		}
		searches = append(searches, saved)
	}
	return searches, rows.Err()
}

// The parent row serializes creates and bulk erasure for this account. Counting
// outside this lock would let concurrent requests exceed the storage quota.
func lockSavedSearchAccount(ctx context.Context, tx *sql.Tx, userID int) error {
	var id int
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSavedSearchNotFound
	}
	return err
}

func (db *DB) CreateSavedSearch(ctx context.Context, userID int, input SavedSearchInput) (SavedSearch, error) {
	name, err := normalizedSearchName(input.Name)
	if err != nil {
		return SavedSearch{}, err
	}
	input.Query = strings.TrimSpace(input.Query)
	if err := ValidateSearchCriteria(input.Query, input.Category, input.Status); err != nil {
		return SavedSearch{}, err
	}
	input.ResearchFilters, err = NormalizeResearchFilters(input.ResearchFilters)
	if err != nil {
		return SavedSearch{}, err
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return SavedSearch{}, err
	}
	defer tx.Rollback()
	if err := lockSavedSearchAccount(ctx, tx, userID); err != nil {
		return SavedSearch{}, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM saved_searches WHERE user_id=$1`, userID).Scan(&count); err != nil {
		return SavedSearch{}, err
	}
	if count >= MaxSavedSearches {
		return SavedSearch{}, ErrSavedSearchLimit
	}
	saved, err := scanSavedSearch(tx.QueryRowContext(ctx, `INSERT INTO saved_searches(user_id,name,query_text,category,status,title_filter,host_filter,discovered_from,discovered_to,collection_tag,result_sort,note_filter,bookmarked_only,review_filter)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id,name,query_text,category,status,created_at,updated_at,title_filter,host_filter,discovered_from,discovered_to,collection_tag,result_sort,note_filter,bookmarked_only,review_filter`, userID, name, input.Query, input.Category, input.Status, input.Title, input.Host, input.From, input.To, input.Tag, input.Sort, input.Note, input.BookmarkedOnly, input.Review))
	if err != nil {
		return SavedSearch{}, savedSearchWriteError(err)
	}
	return saved, tx.Commit()
}

func (db *DB) RenameSavedSearch(ctx context.Context, userID, id int, name string) (SavedSearch, error) {
	name, err := normalizedSearchName(name)
	if err != nil {
		return SavedSearch{}, err
	}
	saved, err := scanSavedSearch(db.Conn.QueryRowContext(ctx, `UPDATE saved_searches SET name=$3,updated_at=CURRENT_TIMESTAMP
		WHERE user_id=$1 AND id=$2 RETURNING id,name,query_text,category,status,created_at,updated_at,title_filter,host_filter,discovered_from,discovered_to,collection_tag,result_sort,note_filter,bookmarked_only,review_filter`, userID, id, name))
	if errors.Is(err, sql.ErrNoRows) {
		return SavedSearch{}, ErrSavedSearchNotFound
	}
	return saved, savedSearchWriteError(err)
}

func (db *DB) DeleteSavedSearch(ctx context.Context, userID, id int) error {
	result, err := db.Conn.ExecContext(ctx, `DELETE FROM saved_searches WHERE user_id=$1 AND id=$2`, userID, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSavedSearchNotFound
	}
	return nil
}

func (db *DB) PurgeSavedSearches(ctx context.Context, userID int) (int64, error) {
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := lockSavedSearchAccount(ctx, tx, userID); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM saved_searches WHERE user_id=$1`, userID)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
