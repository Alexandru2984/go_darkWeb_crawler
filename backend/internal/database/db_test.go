package database

import (
	"testing"
)

func TestNodeStructure(t *testing.T) {
	node := Node{
		ID:           1,
		URL:          "http://test.onion",
		Title:        "Test Title",
		StatusCode:   200,
		ServerHeader: "nginx",
	}

	if node.URL != "http://test.onion" {
		t.Errorf("Expected URL http://test.onion, got %s", node.URL)
	}

	if node.StatusCode != 200 {
		t.Errorf("Expected status code 200, got %d", node.StatusCode)
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
