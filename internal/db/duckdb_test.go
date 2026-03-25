package db

import (
	"context"
	"os"
	"strings"
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

		// Embedding is stored in the DB for semantic search but never returned in query results
		if retrieved.Embedding != nil {
			t.Errorf("Embedding should not be returned in query results, got %d floats", len(retrieved.Embedding))
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

	// Similarity scores should be populated
	if results[0].Similarity == nil {
		t.Fatal("Expected similarity score on first result, got nil")
	}
	if results[1].Similarity == nil {
		t.Fatal("Expected similarity score on second result, got nil")
	}

	// First result should have higher similarity
	if *results[0].Similarity <= *results[1].Similarity {
		t.Errorf("Expected first result similarity (%f) > second (%f)",
			*results[0].Similarity, *results[1].Similarity)
	}

	// Scores should be in valid range
	if *results[0].Similarity < 0 || *results[0].Similarity > 1 {
		t.Errorf("Similarity out of range [0,1]: %f", *results[0].Similarity)
	}
}

func TestSearchWithMinSimilarity(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	// Create two episodes with very different embeddings
	highMatch := make([]float32, 768)
	highMatch[0] = 1.0

	lowMatch := make([]float32, 768)
	lowMatch[1] = 1.0

	ep1 := &models.Episode{
		Content:   "High similarity match",
		Source:    "test",
		Embedding: highMatch,
	}
	ep2 := &models.Episode{
		Content:   "Low similarity match",
		Source:    "test",
		Embedding: lowMatch,
	}

	if err := store.InsertEpisode(ctx, ep1); err != nil {
		t.Fatalf("Failed to insert ep1: %v", err)
	}
	if err := store.InsertEpisode(ctx, ep2); err != nil {
		t.Fatalf("Failed to insert ep2: %v", err)
	}

	// Query embedding very close to highMatch
	queryEmbed := make([]float32, 768)
	queryEmbed[0] = 1.0

	t.Run("no threshold returns all", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			QueryEmbedding: queryEmbed,
			MaxResults:     10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("Expected 2 results, got %d", len(results))
		}
	})

	t.Run("high threshold filters low matches", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			QueryEmbedding: queryEmbed,
			MaxResults:     10,
			MinSimilarity:  0.5,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("Expected 1 result with high threshold, got %d", len(results))
		}
		if results[0].Content != "High similarity match" {
			t.Errorf("Expected high match, got %q", results[0].Content)
		}
	})

	t.Run("very high threshold may return nothing", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			QueryEmbedding: queryEmbed,
			MaxResults:     10,
			MinSimilarity:  1.1, // impossible threshold
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("Expected 0 results with impossible threshold, got %d", len(results))
		}
	})
}

func TestSearchSimilarityNilWithoutQuery(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	ep := &models.Episode{
		Content: "No embedding search",
		Source:  "test",
	}
	if err := store.InsertEpisode(ctx, ep); err != nil {
		t.Fatalf("Failed to insert episode: %v", err)
	}

	results, err := store.Search(ctx, models.SearchParams{
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if results[0].Similarity != nil {
		t.Errorf("Expected nil similarity without query embedding, got %f", *results[0].Similarity)
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

func TestSearchKeywordMode(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	// Insert episodes with distinct content for keyword matching
	episodes := []*models.Episode{
		{Content: "The quick brown fox jumps over the lazy dog", Source: "test", Name: "fox story"},
		{Content: "A lazy cat sleeps on the warm windowsill", Source: "test", Name: "cat nap"},
		{Content: "Quantum computing uses qubits for parallel processing", Source: "test", Name: "quantum"},
	}

	for _, ep := range episodes {
		if err := store.InsertEpisode(ctx, ep); err != nil {
			t.Fatalf("Failed to insert episode: %v", err)
		}
	}

	t.Run("keyword search returns FTS results", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			Query:      "lazy",
			SearchMode: "keyword",
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 2 {
			t.Fatalf("Expected 2 results for 'lazy', got %d", len(results))
		}

		// Both results should contain "lazy"
		for _, r := range results {
			if !containsWord(r.Content, "lazy") && !containsWord(r.Name, "lazy") {
				t.Errorf("Result %q does not contain 'lazy'", r.Content)
			}
		}
	})

	t.Run("keyword search returns no results for non-matching query", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			Query:      "elephants",
			SearchMode: "keyword",
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 0 {
			t.Errorf("Expected 0 results for 'elephants', got %d", len(results))
		}
	})

	t.Run("keyword search does not require embedding", func(t *testing.T) {
		// Keyword search should work without QueryEmbedding
		results, err := store.Search(ctx, models.SearchParams{
			Query:      "quantum",
			SearchMode: "keyword",
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 1 {
			t.Fatalf("Expected 1 result for 'quantum', got %d", len(results))
		}
		if results[0].Content != episodes[2].Content {
			t.Errorf("Expected quantum episode, got %q", results[0].Content)
		}
		// Similarity should be nil for keyword-only search
		if results[0].Similarity != nil {
			t.Errorf("Expected nil similarity for keyword search, got %f", *results[0].Similarity)
		}
	})
}

func TestSearchHybridMode(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	// Create embeddings for semantic similarity
	embed1 := make([]float32, 768)
	embed1[0] = 1.0 // Points in dimension 0

	embed2 := make([]float32, 768)
	embed2[1] = 1.0 // Points in dimension 1

	embed3 := make([]float32, 768)
	embed3[0] = 0.7
	embed3[1] = 0.7 // Between dimensions 0 and 1

	episodes := []*models.Episode{
		{Content: "The quick brown fox jumps over the lazy dog", Source: "test", Embedding: embed1},
		{Content: "A lazy cat sleeps on the warm windowsill", Source: "test", Embedding: embed2},
		{Content: "The fox is quick and not at all lazy today", Source: "test", Embedding: embed3},
	}

	for _, ep := range episodes {
		if err := store.InsertEpisode(ctx, ep); err != nil {
			t.Fatalf("Failed to insert episode: %v", err)
		}
	}

	t.Run("hybrid search combines scores", func(t *testing.T) {
		queryEmbed := make([]float32, 768)
		queryEmbed[0] = 1.0

		results, err := store.Search(ctx, models.SearchParams{
			Query:          "fox",
			QueryEmbedding: queryEmbed,
			SearchMode:     "hybrid",
			SearchAlpha:    0.5,
			MaxResults:     10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		// Should return episodes that match keyword "fox" OR have high cosine similarity
		if len(results) == 0 {
			t.Fatal("Expected results for hybrid search, got 0")
		}
	})

	t.Run("default alpha applies when not specified", func(t *testing.T) {
		queryEmbed := make([]float32, 768)
		queryEmbed[0] = 1.0

		// Verify that omitting SearchAlpha doesn't error and produces results
		// (default alpha=0.7 is applied internally)
		results, err := store.Search(ctx, models.SearchParams{
			Query:          "fox",
			QueryEmbedding: queryEmbed,
			SearchMode:     "hybrid",
			// SearchAlpha omitted — defaults to 0.7
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) == 0 {
			t.Fatal("Expected results with default alpha")
		}

		// All results containing "fox" should be present
		foxCount := 0
		for _, r := range results {
			if containsWord(r.Content, "fox") {
				foxCount++
			}
		}
		if foxCount < 2 {
			t.Errorf("Expected at least 2 results containing 'fox', got %d", foxCount)
		}
	})

	t.Run("alpha=1.0 weights cosine only", func(t *testing.T) {
		queryEmbed := make([]float32, 768)
		queryEmbed[1] = 1.0 // Points toward embed2 (cat episode)

		results, err := store.Search(ctx, models.SearchParams{
			Query:          "fox",
			QueryEmbedding: queryEmbed,
			SearchMode:     "hybrid",
			SearchAlpha:    1.0, // Cosine only
			MaxResults:     10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) == 0 {
			t.Fatal("Expected results with alpha=1.0")
		}

		// With alpha=1.0, cosine similarity fully determines ranking.
		// queryEmbed points in dimension 1, so embed2 (cat) should be first.
		if !containsWord(results[0].Content, "cat") {
			t.Errorf("Expected cat episode first with alpha=1.0, got %q", results[0].Content)
		}
	})
}

func TestSearchDefaultModeUnchanged(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	embed1 := make([]float32, 768)
	embed1[0] = 1.0

	ep := &models.Episode{
		Content:   "Test default mode",
		Source:    "test",
		Embedding: embed1,
	}
	if err := store.InsertEpisode(ctx, ep); err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	// Empty search mode should behave like "vector" (existing behavior)
	queryEmbed := make([]float32, 768)
	queryEmbed[0] = 1.0

	results, err := store.Search(ctx, models.SearchParams{
		QueryEmbedding: queryEmbed,
		MaxResults:     10,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	// Similarity should be populated (vector mode behavior)
	if results[0].Similarity == nil {
		t.Error("Expected similarity to be populated for default (vector) mode")
	}
}

func TestFTSIndexLazyRebuild(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	// Insert first episode
	ep1 := &models.Episode{
		Content: "Original content about databases",
		Source:  "test",
	}
	if err := store.InsertEpisode(ctx, ep1); err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	// Search with keyword mode — should find it (triggers initial FTS index build)
	results, err := store.Search(ctx, models.SearchParams{
		Query:      "databases",
		SearchMode: "keyword",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result after first insert, got %d", len(results))
	}

	// Insert second episode — should mark FTS as stale
	ep2 := &models.Episode{
		Content: "New content about databases and caching",
		Source:  "test",
	}
	if err := store.InsertEpisode(ctx, ep2); err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	// Search again — should rebuild index and find both
	results, err = store.Search(ctx, models.SearchParams{
		Query:      "databases",
		SearchMode: "keyword",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Expected 2 results after second insert and rebuild, got %d", len(results))
	}
}

func TestSearchKeywordWithFilters(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	episodes := []*models.Episode{
		{Content: "Alpha testing in production", Source: "test", GroupID: "group-1", Tags: []string{"dev"}},
		{Content: "Alpha version released today", Source: "test", GroupID: "group-2", Tags: []string{"release"}},
		{Content: "Beta testing started", Source: "test", GroupID: "group-1", Tags: []string{"dev"}},
	}

	for _, ep := range episodes {
		if err := store.InsertEpisode(ctx, ep); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}

	t.Run("keyword with group filter", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			Query:      "Alpha",
			SearchMode: "keyword",
			GroupID:    "group-1",
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("Expected 1 result, got %d", len(results))
		}
	})

	t.Run("keyword with tag filter", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			Query:      "Alpha",
			SearchMode: "keyword",
			Tags:       []string{"release"},
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("Expected 1 result, got %d", len(results))
		}
	})
}

func TestSanitizeFTSQuery(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain text", "hello world", "hello world"},
		{"single quotes escaped", "it's a test", "it''s a test"},
		{"FTS operators stripped", "+foo -bar", "foo bar"},
		{"wildcards stripped", "test*", "test"},
		{"double quotes stripped", `"exact phrase"`, "exact phrase"},
		{"parentheses stripped", "(group) test", "group test"},
		{"backslashes stripped", `path\to\file`, "pathtofile"},
		{"semicolons stripped", "test; DROP TABLE", "test DROP TABLE"},
		{"SQL comments stripped", "test -- comment", "test  comment"},
		{"block comments stripped", "test /* comment */ end", "test  comment  end"},
		{"injection attempt", "'; DROP TABLE episodes; --", "'' DROP TABLE episodes "},
		{"empty string", "", ""},
		{"only special chars", `+-*"();`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFTSQuery(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeFTSQuery(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSearchRelevanceField(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	// Create embeddings with known similarity properties
	embed1 := make([]float32, 768)
	embed1[0] = 1.0 // High match

	embed2 := make([]float32, 768)
	embed2[0] = 0.5
	embed2[1] = 0.5 // Medium match

	embed3 := make([]float32, 768)
	embed3[1] = 1.0 // Low match

	episodes := []*models.Episode{
		{Content: "Highly relevant result", Source: "test", Embedding: embed1},
		{Content: "Moderately relevant result", Source: "test", Embedding: embed2},
		{Content: "Least relevant result", Source: "test", Embedding: embed3},
	}
	for _, ep := range episodes {
		if err := store.InsertEpisode(ctx, ep); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}

	queryEmbed := make([]float32, 768)
	queryEmbed[0] = 1.0

	t.Run("vector mode populates relevance with normalized scores", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			QueryEmbedding: queryEmbed,
			MaxResults:     10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("Expected 3 results, got %d", len(results))
		}

		// All results should have relevance populated
		for i, r := range results {
			if r.Relevance == nil {
				t.Fatalf("Result %d: expected relevance, got nil", i)
			}
			if r.Similarity == nil {
				t.Fatalf("Result %d: expected similarity, got nil", i)
			}
		}

		// Top result should have relevance ~1.0 (normalized max)
		if *results[0].Relevance < 0.9 {
			t.Errorf("Top result relevance should be ~1.0, got %f", *results[0].Relevance)
		}

		// Last result should have relevance ~0.0 (normalized min)
		if *results[2].Relevance > 0.1 {
			t.Errorf("Last result relevance should be ~0.0, got %f", *results[2].Relevance)
		}

		// Raw similarity should still be populated (backwards compatibility)
		if *results[0].Similarity < *results[1].Similarity {
			t.Errorf("Raw similarity ordering broken: %f < %f", *results[0].Similarity, *results[1].Similarity)
		}
	})

	t.Run("no query returns nil relevance", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		for i, r := range results {
			if r.Relevance != nil {
				t.Errorf("Result %d: expected nil relevance without query, got %f", i, *r.Relevance)
			}
		}
	})
}

func TestSearchHybridNormalization(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	embed1 := make([]float32, 768)
	embed1[0] = 1.0

	embed2 := make([]float32, 768)
	embed2[1] = 1.0

	episodes := []*models.Episode{
		{Content: "The fox runs through the forest quickly", Source: "test", Embedding: embed1},
		{Content: "A cat sleeps peacefully on the couch", Source: "test", Embedding: embed2},
	}
	for _, ep := range episodes {
		if err := store.InsertEpisode(ctx, ep); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}

	queryEmbed := make([]float32, 768)
	queryEmbed[0] = 1.0

	results, err := store.Search(ctx, models.SearchParams{
		Query:          "fox",
		QueryEmbedding: queryEmbed,
		SearchMode:     "hybrid",
		SearchAlpha:    0.5,
		MaxResults:     10,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Expected results")
	}

	// All results should have relevance populated
	for i, r := range results {
		if r.Relevance == nil {
			t.Errorf("Result %d: expected relevance in hybrid mode, got nil", i)
		}
	}

	// Fox episode should rank first (both keyword match and cosine match)
	if !containsWord(results[0].Content, "fox") {
		t.Errorf("Expected fox episode first in hybrid mode, got %q", results[0].Content)
	}
}

func TestSearchTagBoost(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	embed1 := make([]float32, 768)
	embed1[0] = 0.7
	embed1[1] = 0.3

	embed2 := make([]float32, 768)
	embed2[0] = 1.0

	// Episode with tags but noticeably lower similarity
	ep1 := &models.Episode{
		Content:   "Tagged and relevant content",
		Source:    "test",
		Tags:      []string{"important", "review"},
		Embedding: embed1,
	}
	// Episode without tags but highest similarity
	ep2 := &models.Episode{
		Content:   "Untagged but very similar content",
		Source:    "test",
		Embedding: embed2,
	}

	store.InsertEpisode(ctx, ep1)
	store.InsertEpisode(ctx, ep2)

	queryEmbed := make([]float32, 768)
	queryEmbed[0] = 1.0

	t.Run("tag_boost=0 hard filters (default behavior)", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			QueryEmbedding: queryEmbed,
			Tags:           []string{"important"},
			MaxResults:     10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		// Hard filter: only tagged episode returned
		if len(results) != 1 {
			t.Fatalf("Expected 1 result with hard filter, got %d", len(results))
		}
		if results[0].Content != ep1.Content {
			t.Errorf("Expected tagged episode, got %q", results[0].Content)
		}
	})

	t.Run("tag_boost>0 returns all but boosts tagged", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			QueryEmbedding: queryEmbed,
			Tags:           []string{"important"},
			TagBoost:       1.5,
			MaxResults:     10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		// Both episodes returned
		if len(results) != 2 {
			t.Fatalf("Expected 2 results with tag boost, got %d", len(results))
		}
		// Tagged episode should rank first due to boost despite lower raw similarity
		if results[0].Content != ep1.Content {
			t.Errorf("Expected tagged episode first with boost, got %q", results[0].Content)
		}
	})

	t.Run("tag_boost with keyword mode", func(t *testing.T) {
		epA := &models.Episode{
			Content: "Alpha database testing framework",
			Source:  "test",
			Tags:    []string{"dev"},
		}
		epB := &models.Episode{
			Content: "Alpha release notes for production",
			Source:  "test",
			Tags:    []string{"release"},
		}
		store.InsertEpisode(ctx, epA)
		store.InsertEpisode(ctx, epB)

		results, err := store.Search(ctx, models.SearchParams{
			Query:      "Alpha",
			SearchMode: "keyword",
			Tags:       []string{"release"},
			TagBoost:   0.5,
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		// Both Alpha episodes should be returned
		if len(results) < 2 {
			t.Fatalf("Expected at least 2 results, got %d", len(results))
		}
		// The "release" tagged one should rank higher
		if !containsWord(results[0].Content, "release") {
			t.Errorf("Expected release episode first with tag boost, got %q", results[0].Content)
		}
	})

	t.Run("partial tag match produces intermediate boost", func(t *testing.T) {
		// Episode with 2/2 tags, 1/2 tags, and 0/2 tags — all same embedding
		sameEmbed := make([]float32, 768)
		sameEmbed[0] = 0.5
		sameEmbed[1] = 0.5

		epFull := &models.Episode{
			Content:   "Full tag match episode for partial test",
			Source:    "test",
			Tags:      []string{"alpha", "beta"},
			Embedding: sameEmbed,
		}
		epPartial := &models.Episode{
			Content:   "Partial tag match episode for partial test",
			Source:    "test",
			Tags:      []string{"alpha"},
			Embedding: sameEmbed,
		}
		epNone := &models.Episode{
			Content:   "No tag match episode for partial test",
			Source:    "test",
			Tags:      []string{"gamma"},
			Embedding: sameEmbed,
		}
		store.InsertEpisode(ctx, epFull)
		store.InsertEpisode(ctx, epPartial)
		store.InsertEpisode(ctx, epNone)

		results, err := store.Search(ctx, models.SearchParams{
			QueryEmbedding: queryEmbed,
			Tags:           []string{"alpha", "beta"},
			TagBoost:       1.0,
			MaxResults:     10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		// Find our three test episodes by content prefix
		var fullRel, partialRel, noneRel *float64
		for _, r := range results {
			if containsWord(r.Content, "Full tag match") {
				fullRel = r.Relevance
			} else if containsWord(r.Content, "Partial tag match") {
				partialRel = r.Relevance
			} else if containsWord(r.Content, "No tag match") {
				noneRel = r.Relevance
			}
		}

		if fullRel == nil || partialRel == nil || noneRel == nil {
			t.Fatalf("Could not find all three test episodes in results (got %d total)", len(results))
		}

		// Full match (2/2 = 1.0 ratio) should beat partial (1/2 = 0.5 ratio)
		if *fullRel <= *partialRel {
			t.Errorf("Full tag match relevance (%f) should exceed partial (%f)", *fullRel, *partialRel)
		}
		// Partial (1/2 = 0.5 ratio) should beat none (0/2 = 0.0 ratio)
		if *partialRel <= *noneRel {
			t.Errorf("Partial tag match relevance (%f) should exceed no match (%f)", *partialRel, *noneRel)
		}
	})
}

func TestRelevanceMonotonic(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	// Insert episodes with tags and embeddings for all modes
	embed1 := make([]float32, 768)
	embed1[0] = 1.0
	embed2 := make([]float32, 768)
	embed2[0] = 0.5
	embed2[1] = 0.5
	embed3 := make([]float32, 768)
	embed3[1] = 1.0

	episodes := []*models.Episode{
		{Content: "Alpha keyword match content", Source: "test", Tags: []string{"target"}, Embedding: embed1},
		{Content: "Beta keyword match content", Source: "test", Tags: []string{"other"}, Embedding: embed2},
		{Content: "Gamma unrelated content here", Source: "test", Embedding: embed3},
	}
	for _, ep := range episodes {
		store.InsertEpisode(ctx, ep)
	}

	queryEmbed := make([]float32, 768)
	queryEmbed[0] = 1.0

	assertMonotonic := func(t *testing.T, results []models.Episode) {
		t.Helper()
		for i := 1; i < len(results); i++ {
			if results[i-1].Relevance == nil || results[i].Relevance == nil {
				continue
			}
			if *results[i-1].Relevance < *results[i].Relevance {
				t.Errorf("Relevance not monotonic: result[%d]=%f < result[%d]=%f",
					i-1, *results[i-1].Relevance, i, *results[i].Relevance)
			}
		}
	}

	t.Run("vector mode with tag_boost", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			QueryEmbedding: queryEmbed,
			Tags:           []string{"target"},
			TagBoost:       0.5,
			MaxResults:     10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		assertMonotonic(t, results)
	})

	t.Run("keyword mode with tag_boost", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			Query:      "keyword",
			SearchMode: "keyword",
			Tags:       []string{"target"},
			TagBoost:   0.5,
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		assertMonotonic(t, results)
	})

	t.Run("hybrid mode with tag_boost", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			Query:          "keyword",
			QueryEmbedding: queryEmbed,
			SearchMode:     "hybrid",
			Tags:           []string{"target"},
			TagBoost:       0.5,
			MaxResults:     10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		assertMonotonic(t, results)
	})
}

func TestSearchKeywordNumericFallback(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	ctx := context.Background()

	episodes := []*models.Episode{
		{Content: "Dev account 123456789012 in us-east-2", Source: "test", GroupID: "g1"},
		{Content: "Prod account 987654321098 in us-east-2", Source: "test", GroupID: "g1"},
		{Content: "Unrelated episode about databases", Source: "test", GroupID: "g1"},
	}
	for _, ep := range episodes {
		if err := store.InsertEpisode(ctx, ep); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}

	t.Run("numeric account ID found via fallback", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			Query:      "123456789012",
			SearchMode: "keyword",
			GroupID:    "g1",
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("Expected 1 result for numeric ID, got %d", len(results))
		}
		if !containsWord(results[0].Content, "123456789012") {
			t.Errorf("Expected episode containing account ID, got %q", results[0].Content)
		}
		if results[0].Relevance == nil || *results[0].Relevance != 1.0 {
			t.Errorf("Expected relevance 1.0 for fallback match, got %v", results[0].Relevance)
		}
	})

	t.Run("text keywords still use BM25 not fallback", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			Query:      "databases",
			SearchMode: "keyword",
			GroupID:    "g1",
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("Expected 1 BM25 result, got %d", len(results))
		}
		if !containsWord(results[0].Content, "databases") {
			t.Errorf("Expected databases episode, got %q", results[0].Content)
		}
	})

	t.Run("fallback respects group filter", func(t *testing.T) {
		results, err := store.Search(ctx, models.SearchParams{
			Query:      "123456789012",
			SearchMode: "keyword",
			GroupID:    "nonexistent",
			MaxResults: 10,
		})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("Expected 0 results with wrong group, got %d", len(results))
		}
	})
}

// containsWord checks if text contains a word (case-insensitive)
func containsWord(text, word string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, strings.ToLower(word))
}

func TestInsertEntity(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	t.Run("creates new entity", func(t *testing.T) {
		entity, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "DuckDB",
			EntityType:    "tool",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to insert entity: %v", err)
		}
		if entity.ID == "" {
			t.Error("Entity ID was not generated")
		}
		if entity.CanonicalName != "DuckDB" {
			t.Errorf("Expected canonical name 'DuckDB', got %q", entity.CanonicalName)
		}
	})

	t.Run("defaults GroupID to default", func(t *testing.T) {
		entity, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "TestEntity",
			EntityType:    "concept",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to insert entity: %v", err)
		}
		if entity.GroupID != "default" {
			t.Errorf("Expected group_id 'default', got %q", entity.GroupID)
		}
	})

	t.Run("case-insensitive resolution", func(t *testing.T) {
		// "duckdb" should resolve to existing "DuckDB"
		resolved, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "duckdb",
			EntityType:    "tool",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to resolve entity: %v", err)
		}
		if resolved.CanonicalName != "DuckDB" {
			t.Errorf("Expected resolution to 'DuckDB', got %q", resolved.CanonicalName)
		}
	})

	t.Run("normalized resolution strips spaces", func(t *testing.T) {
		// Create "OscillateLabs" first
		original, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "OscillateLabs",
			EntityType:    "organization",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}

		// "Oscillate Labs" should resolve to "OscillateLabs" via normalized match
		resolved, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "Oscillate Labs",
			EntityType:    "organization",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to resolve: %v", err)
		}
		if resolved.ID != original.ID {
			t.Errorf("Expected same ID %s, got %s", original.ID, resolved.ID)
		}
	})

	t.Run("normalized resolution handles hyphens and dots", func(t *testing.T) {
		// Create "HomeAssistant"
		original, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "HomeAssistant",
			EntityType:    "tool",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
		// "Home-Assistant" should resolve to "HomeAssistant" (strip hyphen)
		resolved, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "Home-Assistant",
			EntityType:    "tool",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to resolve: %v", err)
		}
		if resolved.ID != original.ID {
			t.Errorf("Expected same ID %s, got %s", original.ID, resolved.ID)
		}
	})

	t.Run("LLC suffix creates distinct entity", func(t *testing.T) {
		// "Oscillate Labs LLC" normalizes to "oscillatelabsllc" while
		// "OscillateLabs" normalizes to "oscillatelabs" — these are
		// genuinely different normalized forms, so distinct entities.
		entity, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "Oscillate Labs LLC",
			EntityType:    "organization",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
		if entity.CanonicalName == "OscillateLabs" {
			t.Error("LLC variant should be a distinct entity from base name")
		}
	})

	t.Run("distinct entities stay separate", func(t *testing.T) {
		python, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "Python",
			EntityType:    "tool",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
		golang, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "Go",
			EntityType:    "tool",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
		if python.ID == golang.ID {
			t.Error("Python and Go should be distinct entities")
		}
	})

	t.Run("different groups are independent", func(t *testing.T) {
		e1, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "SharedName",
			EntityType:    "concept",
			GroupID:       "group-a",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
		e2, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "SharedName",
			EntityType:    "concept",
			GroupID:       "group-b",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
		if e1.ID == e2.ID {
			t.Error("Same name in different groups should be distinct entities")
		}
	})

	t.Run("GetEntity retrieves by ID", func(t *testing.T) {
		entity, err := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "RetrieveMe",
			EntityType:    "person",
		}, 0.88)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}

		retrieved, err := store.GetEntity(ctx, entity.ID)
		if err != nil {
			t.Fatalf("Failed to get entity: %v", err)
		}
		if retrieved.CanonicalName != "RetrieveMe" {
			t.Errorf("Expected 'RetrieveMe', got %q", retrieved.CanonicalName)
		}
	})
}

func TestInsertKnowledgeTriple(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Create entities first
	subject, _ := store.InsertEntity(ctx, &models.Entity{
		CanonicalName: "Engram",
		EntityType:    "project",
	}, 0.88)
	object, _ := store.InsertEntity(ctx, &models.Entity{
		CanonicalName: "DuckDB",
		EntityType:    "tool",
	}, 0.88)

	t.Run("inserts triple with defaults", func(t *testing.T) {
		triple := &models.KnowledgeTriple{
			SubjectEntityID: subject.ID,
			Predicate:       "uses",
			ObjectEntityID:  object.ID,
			Source:          "test",
		}
		err := store.InsertKnowledgeTriple(ctx, triple)
		if err != nil {
			t.Fatalf("Failed to insert triple: %v", err)
		}
		if triple.ID == "" {
			t.Error("Triple ID was not generated")
		}
		if triple.Confidence != 1.0 {
			t.Errorf("Expected default confidence 1.0, got %f", triple.Confidence)
		}
	})

	t.Run("inserts triple with custom confidence", func(t *testing.T) {
		triple := &models.KnowledgeTriple{
			SubjectEntityID: subject.ID,
			Predicate:       "depends_on",
			ObjectEntityID:  object.ID,
			Source:          "dreamer/qwen3:8b",
			Confidence:      0.72,
		}
		err := store.InsertKnowledgeTriple(ctx, triple)
		if err != nil {
			t.Fatalf("Failed to insert triple: %v", err)
		}
		if triple.Confidence != 0.72 {
			t.Errorf("Expected confidence 0.72, got %f", triple.Confidence)
		}
	})
}

func TestInsertEpisodeLink(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Create two episodes
	ep1 := &models.Episode{Content: "Episode about DuckDB", Source: "test"}
	ep2 := &models.Episode{Content: "Another episode about DuckDB", Source: "test"}
	store.InsertEpisode(ctx, ep1)
	store.InsertEpisode(ctx, ep2)

	t.Run("creates link between episodes", func(t *testing.T) {
		link := &models.EpisodeLink{
			SourceEpisodeID: ep1.ID,
			TargetEpisodeID: ep2.ID,
			Relationship:    "same_entity",
		}
		err := store.InsertEpisodeLink(ctx, link)
		if err != nil {
			t.Fatalf("Failed to insert link: %v", err)
		}
		if link.ID == "" {
			t.Error("Link ID was not generated")
		}
		if link.Weight != 1.0 {
			t.Errorf("Expected default weight 1.0, got %f", link.Weight)
		}
	})

	t.Run("deduplicates existing links", func(t *testing.T) {
		// Insert same link again — should not error
		link := &models.EpisodeLink{
			SourceEpisodeID: ep1.ID,
			TargetEpisodeID: ep2.ID,
			Relationship:    "same_entity",
		}
		err := store.InsertEpisodeLink(ctx, link)
		if err != nil {
			t.Fatalf("Duplicate link insert should not error: %v", err)
		}

		// Verify only one link exists
		links, err := store.GetEpisodeLinks(ctx, ep1.ID)
		if err != nil {
			t.Fatalf("Failed to get links: %v", err)
		}
		count := 0
		for _, l := range links {
			if l.Relationship == "same_entity" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("Expected 1 same_entity link, got %d", count)
		}
	})

	t.Run("GetEpisodeLinks returns links in both directions", func(t *testing.T) {
		// Query from target side
		links, err := store.GetEpisodeLinks(ctx, ep2.ID)
		if err != nil {
			t.Fatalf("Failed to get links: %v", err)
		}
		if len(links) == 0 {
			t.Error("Expected to find link from target side")
		}
	})

	t.Run("link with via_entity_id", func(t *testing.T) {
		entity, _ := store.InsertEntity(ctx, &models.Entity{
			CanonicalName: "DuckDB",
			EntityType:    "tool",
		}, 0.88)

		link := &models.EpisodeLink{
			SourceEpisodeID: ep1.ID,
			TargetEpisodeID: ep2.ID,
			Relationship:    "elaborates",
			ViaEntityID:     entity.ID,
		}
		err := store.InsertEpisodeLink(ctx, link)
		if err != nil {
			t.Fatalf("Failed to insert link with via_entity_id: %v", err)
		}

		links, _ := store.GetEpisodeLinks(ctx, ep1.ID)
		found := false
		for _, l := range links {
			if l.Relationship == "elaborates" && l.ViaEntityID == entity.ID {
				found = true
			}
		}
		if !found {
			t.Error("Expected to find link with via_entity_id")
		}
	})
}

func TestNormalizeEntityName(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"OscillateLabs", "oscillatelabs"},
		{"Oscillate Labs", "oscillatelabs"},
		{"Oscillate Labs, LLC", "oscillatelabsllc"},
		{"DuckDB", "duckdb"},
		{"duck-db", "duckdb"},
		{"Mike", "mike"},
		{"mike", "mike"},
		{"", ""},
		{"Hello World 123", "helloworld123"},
	}
	for _, tt := range tests {
		got := normalizeEntityName(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeEntityName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEpisodeLinkUniqueConstraint(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	ep1 := &models.Episode{Content: "First", Source: "test"}
	ep2 := &models.Episode{Content: "Second", Source: "test"}
	store.InsertEpisode(ctx, ep1)
	store.InsertEpisode(ctx, ep2)

	// First insert succeeds
	err := store.InsertEpisodeLink(ctx, &models.EpisodeLink{
		SourceEpisodeID: ep1.ID,
		TargetEpisodeID: ep2.ID,
		Relationship:    "same_entity",
	})
	if err != nil {
		t.Fatalf("First link insert failed: %v", err)
	}

	// Duplicate should succeed silently (INSERT OR IGNORE)
	err = store.InsertEpisodeLink(ctx, &models.EpisodeLink{
		SourceEpisodeID: ep1.ID,
		TargetEpisodeID: ep2.ID,
		Relationship:    "same_entity",
	})
	if err != nil {
		t.Fatalf("Duplicate link insert should not error: %v", err)
	}

	// Different relationship should create a new link
	err = store.InsertEpisodeLink(ctx, &models.EpisodeLink{
		SourceEpisodeID: ep1.ID,
		TargetEpisodeID: ep2.ID,
		Relationship:    "elaborates",
	})
	if err != nil {
		t.Fatalf("Different relationship link failed: %v", err)
	}

	links, _ := store.GetEpisodeLinks(ctx, ep1.ID)
	if len(links) != 2 {
		t.Errorf("Expected 2 links (same_entity + elaborates), got %d", len(links))
	}
}

func TestGraphTablesCreated(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Verify all three graph tables exist
	tables := []string{"entities", "knowledge", "episode_links"}
	for _, table := range tables {
		var count int
		err := store.db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_name = ?", table).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to check table %s: %v", table, err)
		}
		if count == 0 {
			t.Errorf("Table %s was not created", table)
		}
	}
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
