package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/oscillatelabsllc/engram/internal/models"
)

func TestNewStore(t *testing.T) {
	tmpFile := t.TempDir() + "/test.duckdb"

	store, err := NewStore(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	if _, err := os.Stat(tmpFile); os.IsNotExist(err) {
		t.Error("Database file was not created")
	}
}

func TestInsertEpisode(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	t.Run("generates ID and CreatedAt", func(t *testing.T) {
		ep := &models.Episode{
			Content: "Test content",
			Source:  "test-source",
		}

		err := store.InsertEpisode(ctx, ep)
		if err != nil {
			t.Fatalf("Failed to insert episode: %v", err)
		}

		if ep.ID == "" {
			t.Error("ID was not generated")
		}
		if ep.CreatedAt.IsZero() {
			t.Error("CreatedAt was not set")
		}
	})

	t.Run("defaults GroupID to 'default'", func(t *testing.T) {
		ep := &models.Episode{
			Content: "Test content",
			Source:  "test-source",
		}

		store.InsertEpisode(ctx, ep)

		if ep.GroupID != "default" {
			t.Errorf("Expected GroupID 'default', got %q", ep.GroupID)
		}
	})

	t.Run("preserves all fields on round-trip", func(t *testing.T) {
		validAt := time.Now().Add(-1 * time.Hour).Truncate(time.Microsecond)
		embedding := make([]float32, 768)
		for i := range embedding {
			embedding[i] = float32(i) * 0.001
		}

		ep := &models.Episode{
			Content:           "Full content",
			Name:              "Test Episode",
			Source:            "test-source",
			SourceModel:       "test-model",
			SourceDescription: "A test episode",
			GroupID:           "custom-group",
			Tags:              []string{"tag1", "tag2", "tag3"},
			ValidAt:           &validAt,
			Metadata:          `{"key":"value","nested":{"a":1}}`,
			Embedding:         embedding,
		}

		err := store.InsertEpisode(ctx, ep)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}

		retrieved, err := store.GetEpisode(ctx, ep.ID)
		if err != nil {
			t.Fatalf("Failed to retrieve: %v", err)
		}

		// Verify all fields
		if retrieved.Content != ep.Content {
			t.Errorf("Content: got %q, want %q", retrieved.Content, ep.Content)
		}
		if retrieved.Name != ep.Name {
			t.Errorf("Name: got %q, want %q", retrieved.Name, ep.Name)
		}
		if retrieved.Source != ep.Source {
			t.Errorf("Source: got %q, want %q", retrieved.Source, ep.Source)
		}
		if retrieved.SourceModel != ep.SourceModel {
			t.Errorf("SourceModel: got %q, want %q", retrieved.SourceModel, ep.SourceModel)
		}
		if retrieved.SourceDescription != ep.SourceDescription {
			t.Errorf("SourceDescription: got %q, want %q", retrieved.SourceDescription, ep.SourceDescription)
		}
		if retrieved.GroupID != ep.GroupID {
			t.Errorf("GroupID: got %q, want %q", retrieved.GroupID, ep.GroupID)
		}
		if retrieved.Metadata != ep.Metadata {
			t.Errorf("Metadata: got %q, want %q", retrieved.Metadata, ep.Metadata)
		}

		// Tags
		if len(retrieved.Tags) != len(ep.Tags) {
			t.Fatalf("Tags length: got %d, want %d", len(retrieved.Tags), len(ep.Tags))
		}
		for i, tag := range ep.Tags {
			if retrieved.Tags[i] != tag {
				t.Errorf("Tags[%d]: got %q, want %q", i, retrieved.Tags[i], tag)
			}
		}

		// ValidAt
		if retrieved.ValidAt == nil {
			t.Fatal("ValidAt is nil")
		}
		if !retrieved.ValidAt.Equal(validAt) {
			t.Errorf("ValidAt: got %v, want %v", retrieved.ValidAt, validAt)
		}

		// Embedding
		if len(retrieved.Embedding) != len(embedding) {
			t.Fatalf("Embedding length: got %d, want %d", len(retrieved.Embedding), len(embedding))
		}
	})
}

func TestGetEpisode(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	t.Run("returns error for non-existent ID", func(t *testing.T) {
		_, err := store.GetEpisode(ctx, "non-existent-id")
		if err == nil {
			t.Error("Expected error for non-existent episode")
		}
	})
}

func TestSearch(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	// Insert test data with distinct, verifiable content
	episodes := []struct {
		content string
		source  string
		groupID string
		tags    []string
	}{
		{"Alpha content", "source-a", "group-1", []string{"important", "review"}},
		{"Beta content", "source-b", "group-1", []string{"review"}},
		{"Gamma content", "source-a", "group-2", []string{"important"}},
		{"Delta content", "source-c", "group-2", []string{"archive"}},
	}

	insertedIDs := make(map[string]string) // content -> id
	for _, e := range episodes {
		ep := &models.Episode{
			Content: e.content,
			Source:  e.source,
			GroupID: e.groupID,
			Tags:    e.tags,
		}
		if err := store.InsertEpisode(ctx, ep); err != nil {
			t.Fatalf("Failed to insert %q: %v", e.content, err)
		}
		insertedIDs[e.content] = ep.ID
	}

	t.Run("filter by GroupID returns correct episodes", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			GroupID:    "group-1",
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("Expected 2 results, got %d", len(results))
		}

		// Verify we got the right episodes
		contents := map[string]bool{}
		for _, r := range results {
			contents[r.Content] = true
		}
		if !contents["Alpha content"] || !contents["Beta content"] {
			t.Errorf("Expected Alpha and Beta, got %v", contents)
		}
	})

	t.Run("filter by Source returns correct episodes", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			Source:     "source-a",
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("Expected 2 results, got %d", len(results))
		}

		for _, r := range results {
			if r.Source != "source-a" {
				t.Errorf("Got result with source %q, expected source-a", r.Source)
			}
		}
	})

	t.Run("filter by single tag returns episodes with that tag", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			Tags:       []string{"important"},
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("Expected 2 results with 'important' tag, got %d", len(results))
		}

		for _, r := range results {
			hasTag := false
			for _, tag := range r.Tags {
				if tag == "important" {
					hasTag = true
					break
				}
			}
			if !hasTag {
				t.Errorf("Result %q missing 'important' tag, has %v", r.Content, r.Tags)
			}
		}
	})

	t.Run("filter by multiple tags uses AND logic", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			Tags:       []string{"important", "review"},
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 1 {
			t.Errorf("Expected 1 result with both tags, got %d", len(results))
		}
		if len(results) > 0 && results[0].Content != "Alpha content" {
			t.Errorf("Expected Alpha content, got %q", results[0].Content)
		}
	})

	t.Run("MaxResults limits output", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			MaxResults: 2,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("Expected exactly 2 results, got %d", len(results))
		}
	})

	t.Run("combined filters narrow results correctly", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			GroupID:    "group-2",
			Tags:       []string{"important"},
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 1 {
			t.Errorf("Expected 1 result, got %d", len(results))
		}
		if len(results) > 0 && results[0].Content != "Gamma content" {
			t.Errorf("Expected Gamma content, got %q", results[0].Content)
		}
	})
}

func TestSearchWithSemanticSimilarity(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	// Create embeddings that we can reason about
	// Episode 1: embedding near [1, 0, 0, ...]
	// Episode 2: embedding near [0, 1, 0, ...]
	// Query: [1, 0, 0, ...] should rank Episode 1 first

	embed1 := make([]float32, 768)
	embed1[0] = 1.0

	embed2 := make([]float32, 768)
	embed2[1] = 1.0

	ep1 := &models.Episode{
		Content:   "First episode - should match query",
		Source:    "test",
		Embedding: embed1,
	}
	ep2 := &models.Episode{
		Content:   "Second episode - orthogonal to query",
		Source:    "test",
		Embedding: embed2,
	}

	store.InsertEpisode(ctx, ep1)
	store.InsertEpisode(ctx, ep2)

	// Query with embedding similar to ep1
	queryEmbed := make([]float32, 768)
	queryEmbed[0] = 0.9
	queryEmbed[1] = 0.1

	results, err := store.Search(ctx, models.SearchParams{
		QueryEmbedding: queryEmbed,
		MaxResults:     10,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	// First result should be the one most similar to query
	if results[0].Content != ep1.Content {
		t.Errorf("Expected %q first (most similar), got %q", ep1.Content, results[0].Content)
	}
}

func TestSearchWithExpiration(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	// Active episode (no expiration)
	activeEp := &models.Episode{
		Content: "Active episode",
		Source:  "test",
	}
	store.InsertEpisode(ctx, activeEp)

	// Expired episode
	expiredEp := &models.Episode{
		Content: "Expired episode",
		Source:  "test",
	}
	store.InsertEpisode(ctx, expiredEp)

	past := time.Now().Add(-1 * time.Hour)
	store.UpdateEpisode(ctx, expiredEp.ID, models.UpdateParams{
		ExpiredAt: &past,
	})

	t.Run("excludes expired by default", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 1 {
			t.Errorf("Expected 1 active result, got %d", len(results))
		}
		if len(results) > 0 && results[0].Content != "Active episode" {
			t.Errorf("Expected active episode, got %q", results[0].Content)
		}
	})

	t.Run("includes expired when requested", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			MaxResults:     10,
			IncludeExpired: true,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("Expected 2 results including expired, got %d", len(results))
		}
	})
}

func TestUpdateEpisode(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	t.Run("updates tags and persists", func(t *testing.T) {
		ep := &models.Episode{
			Content: "Test content",
			Source:  "test",
			Tags:    []string{"original"},
		}
		store.InsertEpisode(ctx, ep)

		newTags := []string{"updated", "tags"}
		err := store.UpdateEpisode(ctx, ep.ID, models.UpdateParams{
			Tags: &newTags,
		})
		if err != nil {
			t.Fatalf("Update failed: %v", err)
		}

		retrieved, _ := store.GetEpisode(ctx, ep.ID)
		if len(retrieved.Tags) != 2 {
			t.Fatalf("Expected 2 tags, got %d", len(retrieved.Tags))
		}
		if retrieved.Tags[0] != "updated" || retrieved.Tags[1] != "tags" {
			t.Errorf("Tags not updated correctly: %v", retrieved.Tags)
		}
	})

	t.Run("updates metadata and persists", func(t *testing.T) {
		ep := &models.Episode{
			Content:  "Test content",
			Source:   "test",
			Metadata: `{"old": true}`,
		}
		store.InsertEpisode(ctx, ep)

		newMeta := `{"new":true,"version":2}`
		err := store.UpdateEpisode(ctx, ep.ID, models.UpdateParams{
			Metadata: &newMeta,
		})
		if err != nil {
			t.Fatalf("Update failed: %v", err)
		}

		retrieved, _ := store.GetEpisode(ctx, ep.ID)
		if retrieved.Metadata != newMeta {
			t.Errorf("Metadata not updated: got %q, want %q", retrieved.Metadata, newMeta)
		}
	})

	t.Run("returns error for non-existent episode", func(t *testing.T) {
		tags := []string{"test"}
		err := store.UpdateEpisode(ctx, "non-existent", models.UpdateParams{
			Tags: &tags,
		})
		if err == nil {
			t.Error("Expected error for non-existent episode")
		}
	})

	t.Run("returns error when no params provided", func(t *testing.T) {
		ep := &models.Episode{Content: "Test", Source: "test"}
		store.InsertEpisode(ctx, ep)

		err := store.UpdateEpisode(ctx, ep.ID, models.UpdateParams{})
		if err == nil {
			t.Error("Expected error for empty update params")
		}
	})
}

func TestDeleteEpisode(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	t.Run("deletes and episode is gone", func(t *testing.T) {
		ep := &models.Episode{Content: "Test", Source: "test"}
		store.InsertEpisode(ctx, ep)

		err := store.DeleteEpisode(ctx, ep.ID)
		if err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		_, err = store.GetEpisode(ctx, ep.ID)
		if err == nil {
			t.Error("Episode should not exist after deletion")
		}
	})

	t.Run("returns error for non-existent episode", func(t *testing.T) {
		err := store.DeleteEpisode(ctx, "non-existent")
		if err == nil {
			t.Error("Expected error for non-existent episode")
		}
	})
}

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	tmpFile := t.TempDir() + "/test.duckdb"
	store, err := NewStore(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}
	return store
}
