package db

import (
	"context"
	"fmt"
	"testing"

	"github.com/oscillatelabsllc/engram/internal/models"
)

func linkEpisodes(t *testing.T, store *Store, source, target, relationship string) {
	t.Helper()
	err := store.InsertEpisodeLink(context.Background(), &models.EpisodeLink{
		SourceEpisodeID: source, TargetEpisodeID: target, Relationship: relationship,
	})
	if err != nil {
		t.Fatalf("InsertEpisodeLink failed: %v", err)
	}
}

func TestTraverseEpisodeLinks(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Chain: A -> B -> C -> D -> E
	linkEpisodes(t, store, "A", "B", "elaborates")
	linkEpisodes(t, store, "B", "C", "follows_up")
	linkEpisodes(t, store, "C", "D", "same_entity")
	linkEpisodes(t, store, "D", "E", "same_entity")

	t.Run("depth 1 returns direct links only", func(t *testing.T) {
		links, err := store.TraverseEpisodeLinks(ctx, "A", 1)
		if err != nil {
			t.Fatalf("TraverseEpisodeLinks failed: %v", err)
		}
		if len(links) != 1 {
			t.Fatalf("Expected 1 link, got %d", len(links))
		}
		if links[0].SourceEpisodeID != "A" || links[0].TargetEpisodeID != "B" {
			t.Errorf("Wrong link: %+v", links[0])
		}
	})

	t.Run("depth 2 walks one hop further", func(t *testing.T) {
		links, err := store.TraverseEpisodeLinks(ctx, "A", 2)
		if err != nil {
			t.Fatalf("TraverseEpisodeLinks failed: %v", err)
		}
		if len(links) != 2 {
			t.Fatalf("Expected 2 links, got %d", len(links))
		}
	})

	t.Run("traverses both directions", func(t *testing.T) {
		links, err := store.TraverseEpisodeLinks(ctx, "B", 1)
		if err != nil {
			t.Fatalf("TraverseEpisodeLinks failed: %v", err)
		}
		if len(links) != 2 {
			t.Fatalf("Expected 2 links (incoming and outgoing), got %d", len(links))
		}
	})

	t.Run("depth is clamped to 3", func(t *testing.T) {
		links, err := store.TraverseEpisodeLinks(ctx, "A", 10)
		if err != nil {
			t.Fatalf("TraverseEpisodeLinks failed: %v", err)
		}
		// depth 3 from A reaches A-B, B-C, C-D but not D-E
		if len(links) != 3 {
			t.Fatalf("Expected 3 links at clamped depth 3, got %d", len(links))
		}
		for _, l := range links {
			if l.SourceEpisodeID == "D" && l.TargetEpisodeID == "E" {
				t.Error("Depth clamp exceeded: D->E should not be reachable")
			}
		}
	})

	t.Run("depth 0 returns nothing", func(t *testing.T) {
		links, err := store.TraverseEpisodeLinks(ctx, "A", 0)
		if err != nil {
			t.Fatalf("TraverseEpisodeLinks failed: %v", err)
		}
		if len(links) != 0 {
			t.Errorf("Expected no links at depth 0, got %d", len(links))
		}
	})

	t.Run("terminates on cycles", func(t *testing.T) {
		linkEpisodes(t, store, "X", "Y", "same_entity")
		linkEpisodes(t, store, "Y", "X", "same_entity")
		links, err := store.TraverseEpisodeLinks(ctx, "X", 3)
		if err != nil {
			t.Fatalf("TraverseEpisodeLinks failed on cycle: %v", err)
		}
		if len(links) != 2 {
			t.Errorf("Expected 2 distinct links in cycle, got %d", len(links))
		}
	})

	t.Run("caps total fan-out at 50", func(t *testing.T) {
		for i := 0; i < 60; i++ {
			linkEpisodes(t, store, "HUB", fmt.Sprintf("SPOKE-%d", i), "same_entity")
		}
		links, err := store.TraverseEpisodeLinks(ctx, "HUB", 3)
		if err != nil {
			t.Fatalf("TraverseEpisodeLinks failed: %v", err)
		}
		if len(links) != 50 {
			t.Errorf("Expected fan-out capped at 50, got %d", len(links))
		}
	})
}

func TestFindLooseEnds(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Episodes: one fully loose, one linked, one referenced by knowledge
	loose := insertTestEpisode(t, store, &models.Episode{Content: "loose", Source: "test"})
	linked := insertTestEpisode(t, store, &models.Episode{Content: "linked", Source: "test"})
	linked2 := insertTestEpisode(t, store, &models.Episode{Content: "linked2", Source: "test"})
	referenced := insertTestEpisode(t, store, &models.Episode{Content: "referenced", Source: "test"})
	otherGroup := insertTestEpisode(t, store, &models.Episode{Content: "other group", Source: "test", GroupID: "g2"})

	linkEpisodes(t, store, linked.ID, linked2.ID, "same_entity")

	mkEntity := func(name, group string) *models.Entity {
		e, err := store.InsertEntity(ctx, &models.Entity{CanonicalName: name, GroupID: group}, 0.88)
		if err != nil {
			t.Fatalf("InsertEntity failed: %v", err)
		}
		return e
	}
	mkTriple := func(subj, obj, sourceEp string) {
		err := store.InsertKnowledgeTriple(ctx, &models.KnowledgeTriple{
			SubjectEntityID: subj, Predicate: "uses", ObjectEntityID: obj,
			SourceEpisodeID: sourceEp, Source: "test",
		})
		if err != nil {
			t.Fatalf("InsertKnowledgeTriple failed: %v", err)
		}
	}

	// Entities: hub appears in 2 triples, dangling ones in exactly 1
	hub := mkEntity("Hub", "")
	dangling1 := mkEntity("Dangling One", "")
	dangling2 := mkEntity("Dangling Two", "")
	mkTriple(hub.ID, dangling1.ID, referenced.ID)
	mkTriple(hub.ID, dangling2.ID, "")

	t.Run("finds unlinked episodes", func(t *testing.T) {
		res, err := store.FindLooseEnds(ctx, "", 10)
		if err != nil {
			t.Fatalf("FindLooseEnds failed: %v", err)
		}
		ids := map[string]bool{}
		for _, ep := range res.UnlinkedEpisodes {
			ids[ep.ID] = true
		}
		if !ids[loose.ID] {
			t.Error("Loose episode not reported")
		}
		if ids[linked.ID] || ids[linked2.ID] {
			t.Error("Linked episode reported as unlinked")
		}
		if ids[referenced.ID] {
			t.Error("Knowledge-referenced episode reported as unlinked")
		}
	})

	t.Run("finds dangling entities with degree 1", func(t *testing.T) {
		res, err := store.FindLooseEnds(ctx, "", 10)
		if err != nil {
			t.Fatalf("FindLooseEnds failed: %v", err)
		}
		names := map[string]bool{}
		for _, e := range res.DanglingEntities {
			names[e.CanonicalName] = true
		}
		if !names["Dangling One"] || !names["Dangling Two"] {
			t.Errorf("Expected dangling entities, got %v", names)
		}
		if names["Hub"] {
			t.Error("Degree-2 entity reported as dangling")
		}
	})

	t.Run("finds isolated clusters of size <= 3", func(t *testing.T) {
		// Pair cluster (size 2): linked <-> linked2 already exists.
		// Big cluster (size 4): C1-C2-C3-C4 should be excluded.
		linkEpisodes(t, store, "C1", "C2", "same_entity")
		linkEpisodes(t, store, "C2", "C3", "same_entity")
		linkEpisodes(t, store, "C3", "C4", "same_entity")

		res, err := store.FindLooseEnds(ctx, "", 10)
		if err != nil {
			t.Fatalf("FindLooseEnds failed: %v", err)
		}
		if len(res.IsolatedClusters) != 1 {
			t.Fatalf("Expected 1 isolated cluster, got %d: %v", len(res.IsolatedClusters), res.IsolatedClusters)
		}
		members := map[string]bool{}
		for _, id := range res.IsolatedClusters[0] {
			members[id] = true
		}
		if !members[linked.ID] || !members[linked2.ID] || len(members) != 2 {
			t.Errorf("Expected cluster {%s, %s}, got %v", linked.ID, linked2.ID, res.IsolatedClusters[0])
		}
	})

	t.Run("filters by group", func(t *testing.T) {
		res, err := store.FindLooseEnds(ctx, "g2", 10)
		if err != nil {
			t.Fatalf("FindLooseEnds failed: %v", err)
		}
		if len(res.UnlinkedEpisodes) != 1 || res.UnlinkedEpisodes[0].ID != otherGroup.ID {
			t.Errorf("Expected only the g2 episode, got %v", res.UnlinkedEpisodes)
		}
		if len(res.DanglingEntities) != 0 {
			t.Errorf("Expected no g2 dangling entities, got %v", res.DanglingEntities)
		}
	})

	t.Run("group id is not injectable", func(t *testing.T) {
		res, err := store.FindLooseEnds(ctx, "default' OR '1'='1", 10)
		if err != nil {
			t.Fatalf("FindLooseEnds errored on hostile group id: %v", err)
		}
		if len(res.UnlinkedEpisodes) != 0 || len(res.DanglingEntities) != 0 {
			t.Error("Hostile group id matched rows — group filter is injectable")
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			insertTestEpisode(t, store, &models.Episode{Content: fmt.Sprintf("bulk %d", i), Source: "test"})
		}
		res, err := store.FindLooseEnds(ctx, "", 2)
		if err != nil {
			t.Fatalf("FindLooseEnds failed: %v", err)
		}
		if len(res.UnlinkedEpisodes) > 2 {
			t.Errorf("Expected at most 2 unlinked episodes, got %d", len(res.UnlinkedEpisodes))
		}
		if len(res.DanglingEntities) > 2 {
			t.Errorf("Expected at most 2 dangling entities, got %d", len(res.DanglingEntities))
		}
	})
}
