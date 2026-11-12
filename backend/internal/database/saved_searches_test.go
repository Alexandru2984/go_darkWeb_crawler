package database

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestSavedSearchOwnershipNamesAndCascade(t *testing.T) {
	db := newTestDB(t)
	a := mustUser(t, db, "saved-a@example.com")
	b := mustUser(t, db, "saved-b@example.com")
	input := SavedSearchInput{Name: "  Private name  ", Query: "  private words  ", Category: "wiki", Status: "completed"}
	saved, err := db.CreateSavedSearch(t.Context(), a, input)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Name != "Private name" || saved.Query != "private words" {
		t.Fatal("normalization mismatch")
	}
	if _, err = db.CreateSavedSearch(t.Context(), a, SavedSearchInput{Name: "PRIVATE NAME"}); !errors.Is(err, ErrSavedSearchDuplicate) {
		t.Fatal("case-insensitive duplicate accepted")
	}
	if _, err = db.CreateSavedSearch(t.Context(), b, input); err != nil {
		t.Fatal("another account could not use its own name")
	}
	if _, err = db.RenameSavedSearch(t.Context(), b, saved.ID, "Stolen"); !errors.Is(err, ErrSavedSearchNotFound) {
		t.Fatal("cross-account rename accepted")
	}
	if err = db.DeleteSavedSearch(t.Context(), b, saved.ID); !errors.Is(err, ErrSavedSearchNotFound) {
		t.Fatal("cross-account delete accepted")
	}
	renamed, err := db.RenameSavedSearch(t.Context(), a, saved.ID, "Renamed")
	if err != nil || renamed.Query != saved.Query || renamed.Category != saved.Category || renamed.Status != saved.Status {
		t.Fatal("rename changed criteria")
	}
	if n, err := db.PurgeSavedSearches(t.Context(), a); err != nil || n != 1 {
		t.Fatalf("purge count=%d err=%v", n, err)
	}
	if _, err := db.CreateSavedSearch(t.Context(), a, SavedSearchInput{Name: "Cascade"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn.Exec(`DELETE FROM users WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Conn.QueryRow(`SELECT count(*) FROM saved_searches`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("account cascade erased another account or kept deleted-account data")
	}
}

func TestSavedSearchQuotaSerializesConcurrentCreates(t *testing.T) {
	db := newTestDB(t)
	uid := mustUser(t, db, "quota@example.com")
	if _, err := db.Conn.Exec(`INSERT INTO saved_searches(user_id,name) SELECT $1,'Existing '||i FROM generate_series(1,$2) i`, uid, MaxSavedSearches-1); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for i := range 2 {
		wait.Go(func() {
			<-start
			_, err := db.CreateSavedSearch(t.Context(), uid, SavedSearchInput{Name: fmt.Sprintf("Concurrent %d", i)})
			results <- err
		})
	}
	close(start)
	wait.Wait()
	close(results)
	ok, limited := 0, 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrSavedSearchLimit):
			limited++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || limited != 1 {
		t.Fatalf("quota race successes=%d limited=%d", ok, limited)
	}
	list, err := db.ListSavedSearches(t.Context(), uid)
	if err != nil || len(list) != MaxSavedSearches {
		t.Fatal("quota boundary was not maintained")
	}
	if err := db.DeleteSavedSearch(t.Context(), uid, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateSavedSearch(t.Context(), uid, SavedSearchInput{Name: "Replacement"}); err != nil {
		t.Fatal(err)
	}
}

func TestSavedSearchValidationHandlesUnicodeAndRejectsInvalidInput(t *testing.T) {
	if _, err := normalizedSearchName(strings.Repeat("ă", MaxSavedSearchName)); err != nil {
		t.Fatal("valid Unicode name rejected")
	}
	for _, name := range []string{" ", strings.Repeat("ă", MaxSavedSearchName+1), "bad\x00name", "bad\nname"} {
		if _, err := normalizedSearchName(name); !errors.Is(err, ErrSavedSearchName) {
			t.Fatal("invalid name accepted")
		}
	}
	if err := ValidateSearchCriteria(strings.Repeat("文", MaxSearchQuery), "wiki", "completed"); err != nil {
		t.Fatal("200 Unicode characters rejected")
	}
	for _, input := range []SavedSearchInput{{Query: strings.Repeat("文", MaxSearchQuery+1)}, {Query: "bad\x00term"}, {Category: "bad\x00category"}, {Category: strings.Repeat("x", 51)}, {Status: "anything"}} {
		if err := ValidateSearchCriteria(input.Query, input.Category, input.Status); !errors.Is(err, ErrSearchCriteria) {
			t.Fatal("invalid criteria accepted")
		}
	}
}
