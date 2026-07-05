package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/oscillatelabsllc/engram/internal/models"
)

func insertTestEpisode(t *testing.T, store *Store, ep *models.Episode) *models.Episode {
	t.Helper()
	if err := store.InsertEpisode(context.Background(), ep); err != nil {
		t.Fatalf("Failed to insert episode: %v", err)
	}
	return ep
}

func TestListUnenrichedEpisodes(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	base := time.Now().Add(-1 * time.Hour)
	ep1 := insertTestEpisode(t, store, &models.Episode{
		Content: "oldest", Name: "first", Source: "test",
		CreatedAt: base, Metadata: `{"key":"value"}`,
	})
	ep2 := insertTestEpisode(t, store, &models.Episode{
		Content: "middle", Source: "test", CreatedAt: base.Add(time.Minute),
	})
	ep3 := insertTestEpisode(t, store, &models.Episode{
		Content: "newest", Source: "test", CreatedAt: base.Add(2 * time.Minute),
	})

	t.Run("returns unenriched episodes oldest first", func(t *testing.T) {
		episodes, err := store.ListUnenrichedEpisodes(ctx, 10, nil)
		if err != nil {
			t.Fatalf("ListUnenrichedEpisodes failed: %v", err)
		}
		if len(episodes) != 3 {
			t.Fatalf("Expected 3 episodes, got %d", len(episodes))
		}
		if episodes[0].ID != ep1.ID || episodes[1].ID != ep2.ID || episodes[2].ID != ep3.ID {
			t.Errorf("Wrong order: got %s, %s, %s", episodes[0].ID, episodes[1].ID, episodes[2].ID)
		}
		if episodes[0].Content != "oldest" {
			t.Errorf("Expected content 'oldest', got %q", episodes[0].Content)
		}
		if episodes[0].Name != "first" {
			t.Errorf("Expected name 'first', got %q", episodes[0].Name)
		}
		if !strings.Contains(episodes[0].Metadata, `"key"`) {
			t.Errorf("Expected metadata to round-trip, got %q", episodes[0].Metadata)
		}
		if episodes[0].CreatedAt.IsZero() {
			t.Error("Expected created_at to be scanned")
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		episodes, err := store.ListUnenrichedEpisodes(ctx, 1, nil)
		if err != nil {
			t.Fatalf("ListUnenrichedEpisodes failed: %v", err)
		}
		if len(episodes) != 1 {
			t.Fatalf("Expected 1 episode, got %d", len(episodes))
		}
		if episodes[0].ID != ep1.ID {
			t.Errorf("Expected oldest episode first, got %s", episodes[0].ID)
		}
	})

	t.Run("excludes enriched episodes", func(t *testing.T) {
		if err := store.MarkEpisodeEnriched(ctx, ep1.ID, ""); err != nil {
			t.Fatalf("MarkEpisodeEnriched failed: %v", err)
		}
		episodes, err := store.ListUnenrichedEpisodes(ctx, 10, nil)
		if err != nil {
			t.Fatalf("ListUnenrichedEpisodes failed: %v", err)
		}
		if len(episodes) != 2 {
			t.Fatalf("Expected 2 episodes after enrichment, got %d", len(episodes))
		}
		for _, ep := range episodes {
			if ep.ID == ep1.ID {
				t.Error("Enriched episode should not be listed")
			}
		}
	})
}

func TestListUnenrichedEpisodesSkipTags(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	tagged := insertTestEpisode(t, store, &models.Episode{
		Content: "skip me", Source: "test", Tags: []string{"private", "journal"},
	})
	untagged := insertTestEpisode(t, store, &models.Episode{
		Content: "process me", Source: "test", Tags: []string{"journal"},
	})

	t.Run("tagged episode is excluded, untagged sibling is listed", func(t *testing.T) {
		episodes, err := store.ListUnenrichedEpisodes(ctx, 10, []string{"private", "secret"})
		if err != nil {
			t.Fatalf("ListUnenrichedEpisodes failed: %v", err)
		}
		if len(episodes) != 1 {
			t.Fatalf("Expected 1 episode (tagged one skipped), got %d", len(episodes))
		}
		if episodes[0].ID != untagged.ID {
			t.Errorf("Expected untagged episode %s, got %s", untagged.ID, episodes[0].ID)
		}
	})

	t.Run("episode with no tags at all remains eligible", func(t *testing.T) {
		noTags := insertTestEpisode(t, store, &models.Episode{
			Content: "tagless", Source: "test",
		})
		episodes, err := store.ListUnenrichedEpisodes(ctx, 10, []string{"private"})
		if err != nil {
			t.Fatalf("ListUnenrichedEpisodes failed: %v", err)
		}
		found := false
		for _, ep := range episodes {
			if ep.ID == noTags.ID {
				found = true
			}
		}
		if !found {
			t.Error("Episode with NULL tags must not be excluded by the skip filter")
		}

		count, err := store.CountUnenrichedEpisodes(ctx, []string{"private"})
		if err != nil {
			t.Fatalf("CountUnenrichedEpisodes failed: %v", err)
		}
		if count != 2 {
			t.Errorf("Expected 2 (untagged sibling + NULL-tags episode), got %d", count)
		}
	})

	t.Run("empty skip list leaves behavior unchanged", func(t *testing.T) {
		episodes, err := store.ListUnenrichedEpisodes(ctx, 10, nil)
		if err != nil {
			t.Fatalf("ListUnenrichedEpisodes failed: %v", err)
		}
		if len(episodes) != 3 {
			t.Fatalf("Expected all three episodes with empty skip list, got %d", len(episodes))
		}
	})

	t.Run("removing the tag makes the episode eligible again", func(t *testing.T) {
		if err := store.UpdateEpisode(ctx, tagged.ID, models.UpdateParams{Tags: &[]string{}}); err != nil {
			t.Fatalf("UpdateEpisode failed: %v", err)
		}
		episodes, err := store.ListUnenrichedEpisodes(ctx, 10, []string{"private"})
		if err != nil {
			t.Fatalf("ListUnenrichedEpisodes failed: %v", err)
		}
		if len(episodes) != 3 {
			t.Errorf("Expected all three episodes once the skip tag is removed, got %d", len(episodes))
		}
	})
}

func TestCountUnenrichedEpisodesSkipTags(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	insertTestEpisode(t, store, &models.Episode{
		Content: "skip me", Source: "test", Tags: []string{"private"},
	})
	insertTestEpisode(t, store, &models.Episode{
		Content: "count me", Source: "test", Tags: []string{"journal"},
	})

	t.Run("tagged episode is not counted", func(t *testing.T) {
		count, err := store.CountUnenrichedEpisodes(ctx, []string{"private"})
		if err != nil {
			t.Fatalf("CountUnenrichedEpisodes failed: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 (tagged episode excluded), got %d", count)
		}
	})

	t.Run("empty skip list counts everything", func(t *testing.T) {
		count, err := store.CountUnenrichedEpisodes(ctx, nil)
		if err != nil {
			t.Fatalf("CountUnenrichedEpisodes failed: %v", err)
		}
		if count != 2 {
			t.Errorf("Expected 2 with empty skip list, got %d", count)
		}
	})
}

func TestCountUnenrichedEpisodes(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	count, err := store.CountUnenrichedEpisodes(ctx, nil)
	if err != nil {
		t.Fatalf("CountUnenrichedEpisodes failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 on empty store, got %d", count)
	}

	ep := insertTestEpisode(t, store, &models.Episode{Content: "one", Source: "test"})
	insertTestEpisode(t, store, &models.Episode{Content: "two", Source: "test"})

	count, err = store.CountUnenrichedEpisodes(ctx, nil)
	if err != nil {
		t.Fatalf("CountUnenrichedEpisodes failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2, got %d", count)
	}

	if err := store.MarkEpisodeEnriched(ctx, ep.ID, ""); err != nil {
		t.Fatalf("MarkEpisodeEnriched failed: %v", err)
	}
	count, err = store.CountUnenrichedEpisodes(ctx, nil)
	if err != nil {
		t.Fatalf("CountUnenrichedEpisodes failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 after enrichment, got %d", count)
	}
}

func TestMarkEpisodeEnriched(t *testing.T) {
	t.Run("records enrichment error while preserving metadata", func(t *testing.T) {
		store := setupTestStore(t)
		defer store.Close()
		ctx := context.Background()

		ep := insertTestEpisode(t, store, &models.Episode{
			Content: "poison", Source: "test", Metadata: `{"existing":"kept"}`,
		})

		if err := store.MarkEpisodeEnriched(ctx, ep.ID, "llm exploded"); err != nil {
			t.Fatalf("MarkEpisodeEnriched failed: %v", err)
		}

		got, err := store.GetEpisode(ctx, ep.ID)
		if err != nil {
			t.Fatalf("GetEpisode failed: %v", err)
		}
		var meta map[string]interface{}
		if err := json.Unmarshal([]byte(got.Metadata), &meta); err != nil {
			t.Fatalf("Metadata is not valid JSON: %q", got.Metadata)
		}
		if meta["existing"] != "kept" {
			t.Errorf("Existing metadata key clobbered: %v", meta)
		}
		if meta["enrichment_error"] != "llm exploded" {
			t.Errorf("Expected enrichment_error recorded, got %v", meta)
		}

		// stamped enriched despite error
		count, _ := store.CountUnenrichedEpisodes(ctx, nil)
		if count != 0 {
			t.Errorf("Expected episode stamped enriched, %d still unenriched", count)
		}
	})

	t.Run("records enrichment error when no metadata exists", func(t *testing.T) {
		store := setupTestStore(t)
		defer store.Close()
		ctx := context.Background()

		ep := insertTestEpisode(t, store, &models.Episode{Content: "bare", Source: "test"})
		if err := store.MarkEpisodeEnriched(ctx, ep.ID, "parse failure"); err != nil {
			t.Fatalf("MarkEpisodeEnriched failed: %v", err)
		}

		got, err := store.GetEpisode(ctx, ep.ID)
		if err != nil {
			t.Fatalf("GetEpisode failed: %v", err)
		}
		var meta map[string]interface{}
		if err := json.Unmarshal([]byte(got.Metadata), &meta); err != nil {
			t.Fatalf("Metadata is not valid JSON: %q", got.Metadata)
		}
		if meta["enrichment_error"] != "parse failure" {
			t.Errorf("Expected enrichment_error recorded, got %v", meta)
		}
	})

	t.Run("errors on unknown episode", func(t *testing.T) {
		store := setupTestStore(t)
		defer store.Close()

		if err := store.MarkEpisodeEnriched(context.Background(), "nonexistent", ""); err == nil {
			t.Error("Expected error for unknown episode")
		}
	})
}

func TestFindEpisodesSharingEntities(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	ep1 := insertTestEpisode(t, store, &models.Episode{Content: "ep1", Source: "test"})
	ep2 := insertTestEpisode(t, store, &models.Episode{Content: "ep2", Source: "test"})
	ep3 := insertTestEpisode(t, store, &models.Episode{Content: "ep3", Source: "test"})

	mkEntity := func(name string) *models.Entity {
		e, err := store.InsertEntity(ctx, &models.Entity{CanonicalName: name}, 0.88)
		if err != nil {
			t.Fatalf("InsertEntity failed: %v", err)
		}
		return e
	}
	shared := mkEntity("Shared Entity")
	other := mkEntity("Other Entity")
	lonely := mkEntity("Lonely Entity")

	mkTriple := func(subj, obj, sourceEp string) {
		err := store.InsertKnowledgeTriple(ctx, &models.KnowledgeTriple{
			SubjectEntityID: subj, Predicate: "uses", ObjectEntityID: obj,
			SourceEpisodeID: sourceEp, Source: "test",
		})
		if err != nil {
			t.Fatalf("InsertKnowledgeTriple failed: %v", err)
		}
	}
	mkTriple(shared.ID, other.ID, ep1.ID)
	mkTriple(shared.ID, lonely.ID, ep2.ID) // shared as subject in ep2
	mkTriple(other.ID, shared.ID, ep3.ID)  // shared as object in ep3
	mkTriple(lonely.ID, lonely.ID, "")     // no source episode

	t.Run("finds episodes sharing an entity, excluding self", func(t *testing.T) {
		ids, err := store.FindEpisodesSharingEntities(ctx, []string{shared.ID}, ep1.ID)
		if err != nil {
			t.Fatalf("FindEpisodesSharingEntities failed: %v", err)
		}
		if len(ids) != 2 {
			t.Fatalf("Expected 2 episodes, got %d: %v", len(ids), ids)
		}
		found := map[string]bool{}
		for _, id := range ids {
			found[id] = true
		}
		if !found[ep2.ID] || !found[ep3.ID] {
			t.Errorf("Expected ep2 and ep3, got %v", ids)
		}
		if found[ep1.ID] {
			t.Error("Excluded episode returned")
		}
	})

	t.Run("empty entity list returns nothing", func(t *testing.T) {
		ids, err := store.FindEpisodesSharingEntities(ctx, nil, ep1.ID)
		if err != nil {
			t.Fatalf("FindEpisodesSharingEntities failed: %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("Expected no results, got %v", ids)
		}
	})

	t.Run("unrelated entity returns nothing", func(t *testing.T) {
		unrelated := mkEntity("Unrelated")
		ids, err := store.FindEpisodesSharingEntities(ctx, []string{unrelated.ID}, "")
		if err != nil {
			t.Fatalf("FindEpisodesSharingEntities failed: %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("Expected no results, got %v", ids)
		}
	})
}
