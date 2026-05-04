package database

import (
	"strings"
	"testing"
)

func TestNodeStructure(t *testing.T) {
	node := Node{
		ID:           1,
		URL:          "http://test.onion",
		Title:        "Test Title",
		StatusCode:   200,
		ServerHeader: "nginx",
		Category:     "wiki",
	}

	if node.URL != "http://test.onion" {
		t.Errorf("Expected URL http://test.onion, got %s", node.URL)
	}
	if node.StatusCode != 200 {
		t.Errorf("Expected status code 200, got %d", node.StatusCode)
	}
	if node.Category != "wiki" {
		t.Errorf("Expected category wiki, got %s", node.Category)
	}
}

func TestEdgeStructure(t *testing.T) {
	edge := Edge{
		Source: "http://source.onion",
		Target: "http://target.onion",
	}

	if edge.Source != "http://source.onion" {
		t.Errorf("Expected source http://source.onion, got %s", edge.Source)
	}
}

func TestContentHash(t *testing.T) {
	t.Run("identical hash for the same data", func(t *testing.T) {
		h1 := ContentHash("Test Title", "Test content")
		h2 := ContentHash("Test Title", "Test content")
		if h1 != h2 {
			t.Errorf("Hash differs for identical data: %s vs %s", h1, h2)
		}
	})

	t.Run("different hash when content changes", func(t *testing.T) {
		h1 := ContentHash("Title", "Content vechi")
		h2 := ContentHash("Title", "Content nou")
		if h1 == h2 {
			t.Error("Hash identical even though content changed")
		}
	})

	t.Run("different hash when title changes", func(t *testing.T) {
		h1 := ContentHash("Titlu vechi", "Acelasi continut")
		h2 := ContentHash("Titlu nou", "Acelasi continut")
		if h1 == h2 {
			t.Error("Hash identical even though title changed — bug in change detection")
		}
	})

	t.Run("non-empty hash for empty data", func(t *testing.T) {
		h := ContentHash("", "")
		if h == "" {
			t.Error("Empty hash for empty data — must return a valid hash")
		}
		// sha256 always produces 64 hex chars
		if len(h) != 64 {
			t.Errorf("Incorrect hash length: expected 64 chars, got %d", len(h))
		}
	})

	t.Run("hash is not vulnerable to title+content ambiguity", func(t *testing.T) {
		// "AB" + "|" + "C" != "A" + "|" + "BC"
		h1 := ContentHash("AB", "C")
		h2 := ContentHash("A", "BC")
		if h1 == h2 {
			t.Error("Hash collision between title='AB',content='C' and title='A',content='BC'")
		}
	})
}

func TestNodeDetailContainsAllFields(t *testing.T) {
	nd := NodeDetail{
		Node: Node{
			ID:               1,
			URL:              "http://test.onion",
			Title:            "Title",
			ProcessingStatus: "completed",
			Category:         "forum",
		},
		Content:      strings.Repeat("x", 100),
		Metadata:     `{"description":"test"}`,
		ContentHash:  "abc123",
		DiscoveredAt: "2024-01-01 00:00:00",
	}

	if nd.Content == "" {
		t.Error("NodeDetail.Content must not be empty")
	}
	if nd.ContentHash == "" {
		t.Error("NodeDetail.ContentHash must not be empty")
	}
	if nd.Category != "forum" {
		t.Errorf("NodeDetail.Category incorrect: %s", nd.Category)
	}
}
