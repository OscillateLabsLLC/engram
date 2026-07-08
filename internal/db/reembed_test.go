package db

import (
	"context"
	"testing"
	"time"

	"github.com/oscillatelabsllc/engram/internal/models"
)

func makeEmbedding(dim0 float32) []float32 {
	emb := make([]float32, 768)
	emb[0] = dim0
	return emb
}

func TestCountReembedTargets(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// One episode embedded with the current model, one with an old model,
	// one legacy row (embedding but no stamp), one with no embedding at all
	episodes := []*models.Episode{
		{Content: "current model", Source: "test", Embedding: makeEmbedding(1), EmbeddingModel: "new-model"},
		{Content: "old model", Source: "test", Embedding: makeEmbedding(1), EmbeddingModel: "old-model"},
		{Content: "legacy unstamped", Source: "test", Embedding: makeEmbedding(1)},
		{Content: "never embedded", Source: "test"},
	}
	for _, ep := range episodes {
		if err := store.InsertEpisode(ctx, ep); err != nil {
			t.Fatalf("Failed to insert episode: %v", err)
		}
	}

	t.Run("counts stale rows only", func(t *testing.T) {
		counts, err := store.CountReembedTargets(ctx, "new-model", false)
		if err != nil {
			t.Fatalf("CountReembedTargets failed: %v", err)
		}
		if counts.Episodes != 3 {
			t.Errorf("Expected 3 stale episodes, got %d", counts.Episodes)
		}
	})

	t.Run("force counts all rows", func(t *testing.T) {
		counts, err := store.CountReembedTargets(ctx, "new-model", true)
		if err != nil {
			t.Fatalf("CountReembedTargets failed: %v", err)
		}
		if counts.Episodes != 4 {
			t.Errorf("Expected 4 episodes with force, got %d", counts.Episodes)
		}
	})
}

func TestListEpisodesForReembed(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	fresh := &models.Episode{Content: "fresh", Source: "test", Embedding: makeEmbedding(1), EmbeddingModel: "new-model"}
	stale := &models.Episode{Content: "stale", Source: "test", Embedding: makeEmbedding(1), EmbeddingModel: "old-model"}
	missing := &models.Episode{Content: "missing", Source: "test"}
	for _, ep := range []*models.Episode{fresh, stale, missing} {
		if err := store.InsertEpisode(ctx, ep); err != nil {
			t.Fatalf("Failed to insert episode: %v", err)
		}
	}

	t.Run("returns only stale episodes with content", func(t *testing.T) {
		items, err := store.ListEpisodesForReembed(ctx, "new-model", "", 10, false)
		if err != nil {
			t.Fatalf("ListEpisodesForReembed failed: %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("Expected 2 stale episodes, got %d", len(items))
		}
		for _, it := range items {
			if it.ID == fresh.ID {
				t.Error("Fresh episode should not be listed")
			}
			if it.Text == "" {
				t.Error("Item text should carry episode content")
			}
		}
	})

	t.Run("keyset pagination advances past processed rows", func(t *testing.T) {
		first, err := store.ListEpisodesForReembed(ctx, "new-model", "", 1, false)
		if err != nil || len(first) != 1 {
			t.Fatalf("Expected 1 item, got %d (err %v)", len(first), err)
		}
		second, err := store.ListEpisodesForReembed(ctx, "new-model", first[0].ID, 10, false)
		if err != nil {
			t.Fatalf("Second page failed: %v", err)
		}
		for _, it := range second {
			if it.ID == first[0].ID {
				t.Error("Keyset pagination returned an already-seen row")
			}
		}
		if len(second) != 1 {
			t.Errorf("Expected 1 item on second page, got %d", len(second))
		}
	})

	t.Run("force lists every episode", func(t *testing.T) {
		items, err := store.ListEpisodesForReembed(ctx, "new-model", "", 10, true)
		if err != nil {
			t.Fatalf("ListEpisodesForReembed force failed: %v", err)
		}
		if len(items) != 3 {
			t.Errorf("Expected 3 episodes with force, got %d", len(items))
		}
	})
}

func TestUpdateEpisodeEmbedding(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	ep := &models.Episode{Content: "re-embed me", Source: "test", Embedding: makeEmbedding(1), EmbeddingModel: "old-model"}
	if err := store.InsertEpisode(ctx, ep); err != nil {
		t.Fatalf("Failed to insert episode: %v", err)
	}

	t.Run("updates vector and stamps model", func(t *testing.T) {
		if err := store.UpdateEpisodeEmbedding(ctx, ep.ID, makeEmbedding(2), "new-model"); err != nil {
			t.Fatalf("UpdateEpisodeEmbedding failed: %v", err)
		}

		counts, err := store.CountReembedTargets(ctx, "new-model", false)
		if err != nil {
			t.Fatalf("CountReembedTargets failed: %v", err)
		}
		if counts.Episodes != 0 {
			t.Errorf("Episode should no longer be stale, got %d stale", counts.Episodes)
		}
	})

	t.Run("errors on unknown id", func(t *testing.T) {
		if err := store.UpdateEpisodeEmbedding(ctx, "no-such-id", makeEmbedding(2), "new-model"); err == nil {
			t.Error("Expected error for unknown episode id")
		}
	})
}

func TestReembedSkipsExpiredRows(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	past := time.Now().Add(-time.Hour)
	// An expired episode that was never embedded (e.g. content over the
	// model's context window, split into smaller episodes and retired)
	expired := &models.Episode{Content: "expired never embedded", Source: "test", ExpiredAt: &past}
	stale := &models.Episode{Content: "live stale", Source: "test"}
	for _, ep := range []*models.Episode{expired, stale} {
		if err := store.InsertEpisode(ctx, ep); err != nil {
			t.Fatalf("Failed to insert episode: %v", err)
		}
	}

	t.Run("expired rows are not counted stale", func(t *testing.T) {
		counts, err := store.CountReembedTargets(ctx, "new-model", false)
		if err != nil {
			t.Fatalf("CountReembedTargets failed: %v", err)
		}
		if counts.Episodes != 1 {
			t.Errorf("Expected 1 stale episode (expired excluded), got %d", counts.Episodes)
		}
	})

	t.Run("expired rows are not counted with force", func(t *testing.T) {
		counts, err := store.CountReembedTargets(ctx, "new-model", true)
		if err != nil {
			t.Fatalf("CountReembedTargets failed: %v", err)
		}
		if counts.Episodes != 1 {
			t.Errorf("Expected 1 episode with force (expired excluded), got %d", counts.Episodes)
		}
	})

	t.Run("expired rows are not listed for re-embed", func(t *testing.T) {
		items, err := store.ListEpisodesForReembed(ctx, "new-model", "", 10, false)
		if err != nil {
			t.Fatalf("ListEpisodesForReembed failed: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("Expected 1 episode listed, got %d", len(items))
		}
		if items[0].ID != stale.ID {
			t.Errorf("Expected the live stale episode, got %s", items[0].ID)
		}
	})
}
