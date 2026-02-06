package models

import (
	"testing"
	"time"
)

func TestEpisodeCreation(t *testing.T) {
	now := time.Now()
	ep := Episode{
		ID:        "test-id",
		Content:   "Test content",
		Source:    "test",
		GroupID:   "default",
		Tags:      []string{"test", "example"},
		CreatedAt: now,
	}

	if ep.ID != "test-id" {
		t.Errorf("Expected ID to be test-id, got %s", ep.ID)
	}

	if ep.GroupID != "default" {
		t.Errorf("Expected GroupID to be default, got %s", ep.GroupID)
	}

	if len(ep.Tags) != 2 {
		t.Errorf("Expected 2 tags, got %d", len(ep.Tags))
	}
}

func TestSearchParams(t *testing.T) {
	params := SearchParams{
		Query:      "test query",
		GroupID:    "default",
		MaxResults: 10,
		Tags:       []string{"important"},
	}

	if params.Query != "test query" {
		t.Errorf("Expected query to be 'test query', got %s", params.Query)
	}

	if params.MaxResults != 10 {
		t.Errorf("Expected MaxResults to be 10, got %d", params.MaxResults)
	}

	if params.IncludeExpired {
		t.Error("Expected IncludeExpired to be false by default")
	}
}
