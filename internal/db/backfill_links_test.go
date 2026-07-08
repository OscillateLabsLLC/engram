package db

import (
	"context"
	"testing"

	"github.com/oscillatelabsllc/engram/internal/models"
)

// TestBackfillEpisodeLinks proves the batch derivation reproduces the dreamer's
// same_entity linking from existing triples: episodes sharing an entity get
// linked both ways, hub entities over the cap are skipped, and reruns are
// idempotent.
func TestBackfillEpisodeLinks(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	mkEntity := func(name string) *models.Entity {
		e, err := store.InsertEntity(ctx, &models.Entity{CanonicalName: name}, 0.88)
		if err != nil {
			t.Fatalf("InsertEntity(%s): %v", name, err)
		}
		return e
	}
	mkTriple := func(subj, obj, sourceEp string) {
		if err := store.InsertKnowledgeTriple(ctx, &models.KnowledgeTriple{
			SubjectEntityID: subj, Predicate: "uses", ObjectEntityID: obj,
			SourceEpisodeID: sourceEp, Source: "test",
		}); err != nil {
			t.Fatalf("InsertKnowledgeTriple: %v", err)
		}
	}

	// Three episodes share entity A; a fourth is isolated (its own entity only).
	epA := insertTestEpisode(t, store, &models.Episode{Content: "epA", Source: "test"})
	epB := insertTestEpisode(t, store, &models.Episode{Content: "epB", Source: "test"})
	epC := insertTestEpisode(t, store, &models.Episode{Content: "epC", Source: "test"})
	epLone := insertTestEpisode(t, store, &models.Episode{Content: "epLone", Source: "test"})

	shared := mkEntity("Shared")
	otherA := mkEntity("OtherA")
	otherB := mkEntity("OtherB")
	otherC := mkEntity("OtherC")
	lonelyEnt := mkEntity("Lonely")

	mkTriple(shared.ID, otherA.ID, epA.ID)
	mkTriple(shared.ID, otherB.ID, epB.ID)
	mkTriple(otherC.ID, shared.ID, epC.ID)
	mkTriple(lonelyEnt.ID, lonelyEnt.ID, epLone.ID) // isolated: only epLone touches it

	stats, err := store.BackfillEpisodeLinks(ctx)
	if err != nil {
		t.Fatalf("BackfillEpisodeLinks: %v", err)
	}

	// epA, epB, epC pairwise via shared = 3*2 = 6 directed links.
	if stats.LinksInserted != 6 {
		t.Errorf("expected 6 links inserted, got %d (stats %+v)", stats.LinksInserted, stats)
	}

	// Traversal from epA must reach epB and epC.
	links, err := store.TraverseEpisodeLinks(ctx, epA.ID, 1)
	if err != nil {
		t.Fatalf("TraverseEpisodeLinks: %v", err)
	}
	reached := map[string]bool{}
	for _, l := range links {
		reached[l.SourceEpisodeID] = true
		reached[l.TargetEpisodeID] = true
	}
	if !reached[epB.ID] || !reached[epC.ID] {
		t.Errorf("epA should link to epB and epC; reached=%v", reached)
	}
	if reached[epLone.ID] {
		t.Errorf("isolated episode epLone must not be linked")
	}

	// Idempotent: a second run inserts nothing new.
	stats2, err := store.BackfillEpisodeLinks(ctx)
	if err != nil {
		t.Fatalf("BackfillEpisodeLinks rerun: %v", err)
	}
	if stats2.LinksInserted != 0 {
		t.Errorf("rerun should insert 0 links, got %d", stats2.LinksInserted)
	}
}

// TestBackfillEpisodeLinksHubCap verifies an entity shared by more than
// backfillHubCap episodes is skipped rather than exploding into O(n^2) links.
func TestBackfillEpisodeLinksHubCap(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	hub, err := store.InsertEntity(ctx, &models.Entity{CanonicalName: "Hub"}, 0.88)
	if err != nil {
		t.Fatalf("InsertEntity: %v", err)
	}

	// backfillHubCap+1 episodes all reference the hub → over the cap. Each
	// object entity is uniquely named so only the hub is widely shared.
	for i := 0; i < backfillHubCap+1; i++ {
		ep := insertTestEpisode(t, store, &models.Episode{Content: "hubep", Source: "test"})
		obj, err := store.InsertEntity(ctx, &models.Entity{CanonicalName: "Obj-" + ep.ID}, 0.88)
		if err != nil {
			t.Fatalf("InsertEntity obj: %v", err)
		}
		if err := store.InsertKnowledgeTriple(ctx, &models.KnowledgeTriple{
			SubjectEntityID: hub.ID, Predicate: "uses", ObjectEntityID: obj.ID,
			SourceEpisodeID: ep.ID, Source: "test",
		}); err != nil {
			t.Fatalf("InsertKnowledgeTriple: %v", err)
		}
	}

	stats, err := store.BackfillEpisodeLinks(ctx)
	if err != nil {
		t.Fatalf("BackfillEpisodeLinks: %v", err)
	}
	if stats.HubsSkipped < 1 {
		t.Errorf("expected the hub entity to be skipped, got HubsSkipped=%d", stats.HubsSkipped)
	}
	if stats.LinksInserted != 0 {
		t.Errorf("hub-only corpus should produce 0 links, got %d", stats.LinksInserted)
	}
}

// TestPruneHubLinks proves stale same_entity links through a now-over-cap entity
// are removed on a subsequent backfill, while typed edges survive.
func TestPruneHubLinks(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Manually insert a same_entity link via a "hub" entity plus a typed edge.
	epA := insertTestEpisode(t, store, &models.Episode{Content: "a", Source: "test"})
	epB := insertTestEpisode(t, store, &models.Episode{Content: "b", Source: "test"})

	hub, err := store.InsertEntity(ctx, &models.Entity{CanonicalName: "HubTopic"}, 0.88)
	if err != nil {
		t.Fatalf("InsertEntity: %v", err)
	}
	// Make hub clearly over the cap: reference it from 15 episodes, each also
	// with a unique object entity. The many singleton objects keep the p90 of
	// the distribution at the floor (6), so the 15-episode hub exceeds it.
	for i := 0; i < 15; i++ {
		ep := insertTestEpisode(t, store, &models.Episode{Content: "hubep", Source: "test"})
		obj, _ := store.InsertEntity(ctx, &models.Entity{CanonicalName: "O-" + ep.ID}, 0.88)
		store.InsertKnowledgeTriple(ctx, &models.KnowledgeTriple{
			SubjectEntityID: hub.ID, Predicate: "uses", ObjectEntityID: obj.ID,
			SourceEpisodeID: ep.ID, Source: "test",
		})
	}

	// Stale same_entity link through the hub, and a typed edge (no via entity).
	store.InsertEpisodeLink(ctx, &models.EpisodeLink{
		SourceEpisodeID: epA.ID, TargetEpisodeID: epB.ID,
		Relationship: "same_entity", ViaEntityID: hub.ID,
	})
	store.InsertEpisodeLink(ctx, &models.EpisodeLink{
		SourceEpisodeID: epA.ID, TargetEpisodeID: epB.ID,
		Relationship: "supersedes",
	})

	stats, err := store.BackfillEpisodeLinks(ctx)
	if err != nil {
		t.Fatalf("BackfillEpisodeLinks: %v", err)
	}
	if stats.HubLinksPruned < 1 {
		t.Errorf("expected the stale hub link to be pruned, got %d (cap=%d)", stats.HubLinksPruned, stats.HubCap)
	}

	// The typed supersedes edge must survive.
	links, _ := store.GetEpisodeLinks(ctx, epA.ID)
	var sawTyped, sawHub bool
	for _, l := range links {
		if l.Relationship == "supersedes" {
			sawTyped = true
		}
		if l.Relationship == "same_entity" && l.ViaEntityID == hub.ID {
			sawHub = true
		}
	}
	if !sawTyped {
		t.Error("typed supersedes edge must survive hub pruning")
	}
	if sawHub {
		t.Error("stale hub same_entity link should have been pruned")
	}
}
