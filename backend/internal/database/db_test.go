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
	t.Run("hash identic pentru aceleasi date", func(t *testing.T) {
		h1 := ContentHash("Test Title", "Test content")
		h2 := ContentHash("Test Title", "Test content")
		if h1 != h2 {
			t.Errorf("Hash diferit pentru date identice: %s vs %s", h1, h2)
		}
	})

	t.Run("hash diferit cand se schimba continutul", func(t *testing.T) {
		h1 := ContentHash("Title", "Content vechi")
		h2 := ContentHash("Title", "Content nou")
		if h1 == h2 {
			t.Error("Hash identic desi continutul s-a schimbat")
		}
	})

	t.Run("hash diferit cand se schimba titlul", func(t *testing.T) {
		h1 := ContentHash("Titlu vechi", "Acelasi continut")
		h2 := ContentHash("Titlu nou", "Acelasi continut")
		if h1 == h2 {
			t.Error("Hash identic desi titlul s-a schimbat — bug in detectia schimbarilor")
		}
	})

	t.Run("hash non-empty pentru date goale", func(t *testing.T) {
		h := ContentHash("", "")
		if h == "" {
			t.Error("Hash gol pentru date goale — trebuie sa returneze un hash valid")
		}
		// sha256 produce intotdeauna 64 hex chars
		if len(h) != 64 {
			t.Errorf("Hash incorect ca lungime: asteptat 64 chars, primit %d", len(h))
		}
	})

	t.Run("hash nu este vulnerabil la title+content ambiguity", func(t *testing.T) {
		// "AB" + "|" + "C" != "A" + "|" + "BC"
		h1 := ContentHash("AB", "C")
		h2 := ContentHash("A", "BC")
		if h1 == h2 {
			t.Error("Hash colideaza intre title='AB',content='C' si title='A',content='BC'")
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
		t.Error("NodeDetail.Content nu trebuie sa fie gol")
	}
	if nd.ContentHash == "" {
		t.Error("NodeDetail.ContentHash nu trebuie sa fie gol")
	}
	if nd.Category != "forum" {
		t.Errorf("NodeDetail.Category incorect: %s", nd.Category)
	}
}
