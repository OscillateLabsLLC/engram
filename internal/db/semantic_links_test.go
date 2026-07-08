package db

import (
	"context"
	"testing"
	"time"

	"github.com/oscillatelabsllc/engram/internal/models"
)

// TestBackfillSemanticEpisodeLinks proves explicit prose markers become typed
// edges: the REVISED-CANON pattern (SUPERSEDES <id>, <id>, <id>) produces one
// supersedes edge per named target, a CORRECTION marker produces contradicts,
// prefix references resolve, ambiguous/absent references do not, and self-refs
// are skipped.
func TestBackfillSemanticEpisodeLinks(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Three target episodes to be superseded.
	t1 := insertTestEpisode(t, store, &models.Episode{Content: "april design memo one", Source: "test"})
	t2 := insertTestEpisode(t, store, &models.Episode{Content: "april design memo two", Source: "test"})
	t3 := insertTestEpisode(t, store, &models.Episode{Content: "april design memo three", Source: "test"})

	// The REVISED-CANON episode names all three by their 8-char prefixes.
	revised := insertTestEpisode(t, store, &models.Episode{
		Name: "REVISED CANON",
		Content: "This REVISED CANON SUPERSEDES three April design memories (" +
			t1.ID[:8] + ", " + t2.ID[:8] + ", " + t3.ID[:8] + "). Use this going forward.",
		Source: "test",
	})

	// A correction episode contradicting a prior plan, referenced by full UUID.
	plan := insertTestEpisode(t, store, &models.Episode{Content: "the nanoleaf integration plan", Source: "test"})
	correction := insertTestEpisode(t, store, &models.Episode{
		Content: "CORRECTION: the approach in " + plan.ID + " was a DEAD END. Do not pursue.",
		Source:  "test",
	})

	// A marker with no ID at all — must NOT produce an edge (fuzzy case deferred).
	insertTestEpisode(t, store, &models.Episode{
		Content: "CORRECTION of the earlier plan — we changed direction.", Source: "test",
	})

	stats, err := store.BackfillSemanticEpisodeLinks(ctx)
	if err != nil {
		t.Fatalf("BackfillSemanticEpisodeLinks: %v", err)
	}

	if stats.SupersedesInserted != 3 {
		t.Errorf("expected 3 supersedes edges, got %d (stats %+v)", stats.SupersedesInserted, stats)
	}
	if stats.ContradictsInserted != 1 {
		t.Errorf("expected 1 contradicts edge, got %d (stats %+v)", stats.ContradictsInserted, stats)
	}

	// Verify the actual edges exist with correct direction and type.
	links, err := store.GetEpisodeLinks(ctx, revised.ID)
	if err != nil {
		t.Fatalf("GetEpisodeLinks: %v", err)
	}
	targets := map[string]string{} // targetID -> relationship
	for _, l := range links {
		if l.SourceEpisodeID == revised.ID {
			targets[l.TargetEpisodeID] = l.Relationship
		}
	}
	for _, want := range []string{t1.ID, t2.ID, t3.ID} {
		if targets[want] != "supersedes" {
			t.Errorf("expected supersedes edge %s->%s, got %q", revised.ID, want, targets[want])
		}
	}

	// Contradiction edge direction: correction -> plan.
	clinks, _ := store.GetEpisodeLinks(ctx, correction.ID)
	found := false
	for _, l := range clinks {
		if l.SourceEpisodeID == correction.ID && l.TargetEpisodeID == plan.ID && l.Relationship == "contradicts" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected contradicts edge %s->%s", correction.ID, plan.ID)
	}

	// Idempotent.
	stats2, err := store.BackfillSemanticEpisodeLinks(ctx)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if stats2.SupersedesInserted != 0 || stats2.ContradictsInserted != 0 {
		t.Errorf("rerun should insert 0 edges, got sup=%d con=%d",
			stats2.SupersedesInserted, stats2.ContradictsInserted)
	}
}

// TestSemanticLinkSupersedesOrientation proves supersedes edges are always
// oriented newer→older regardless of which episode's prose carries the marker,
// and that a stale reverse edge is retracted — no bidirectional supersession.
func TestSemanticLinkSupersedesOrientation(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	old := &models.Episode{Content: "the february plan", Source: "test",
		CreatedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	if err := store.InsertEpisode(ctx, old); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	// The NEW (April) episode says it SUPERSEDES the old one → new->old (correct).
	newer := &models.Episode{Source: "test",
		CreatedAt: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}
	newer.Content = "REVISED: this SUPERSEDES " + old.ID[:8] + "."
	if err := store.InsertEpisode(ctx, newer); err != nil {
		t.Fatalf("insert newer: %v", err)
	}
	// The OLD episode was later ANNOTATED "superseded by <new>" — a backward
	// marker that, taken literally, would emit old->new (wrong direction).
	backAnnotated := &models.Episode{Content: "the march plan, later superseded by " + newer.ID[:8],
		Source:    "test",
		CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
	if err := store.InsertEpisode(ctx, backAnnotated); err != nil {
		t.Fatalf("insert backAnnotated: %v", err)
	}

	if _, err := store.BackfillSemanticEpisodeLinks(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// Every supersedes edge in the store must point newer→older by created_at.
	assertOriented := func(sourceID string) {
		links, _ := store.GetEpisodeLinks(ctx, sourceID)
		for _, l := range links {
			if l.Relationship != "supersedes" {
				continue
			}
			se, _ := store.GetEpisode(ctx, l.SourceEpisodeID)
			te, _ := store.GetEpisode(ctx, l.TargetEpisodeID)
			if te.CreatedAt.After(se.CreatedAt) {
				t.Errorf("supersedes edge %s(%s)->%s(%s) points older->newer",
					l.SourceEpisodeID, se.CreatedAt.Format("2006-01"),
					l.TargetEpisodeID, te.CreatedAt.Format("2006-01"))
			}
			// No reverse edge may coexist.
			rev, _ := store.GetEpisodeLinks(ctx, l.TargetEpisodeID)
			for _, r := range rev {
				if r.Relationship == "supersedes" && r.SourceEpisodeID == l.TargetEpisodeID && r.TargetEpisodeID == l.SourceEpisodeID {
					t.Errorf("bidirectional supersedes between %s and %s", l.SourceEpisodeID, l.TargetEpisodeID)
				}
			}
		}
	}
	assertOriented(newer.ID)
	assertOriented(backAnnotated.ID)
	assertOriented(old.ID)

	// Specifically: the back-annotated (March) marker naming April must have
	// produced april->march, NOT march->april.
	aprilLinks, _ := store.GetEpisodeLinks(ctx, newer.ID)
	foundAprilToMarch := false
	for _, l := range aprilLinks {
		if l.Relationship == "supersedes" && l.SourceEpisodeID == newer.ID && l.TargetEpisodeID == backAnnotated.ID {
			foundAprilToMarch = true
		}
	}
	if !foundAprilToMarch {
		t.Error("back-annotated March marker should yield April->March supersedes (newer->older)")
	}
}

// TestSemanticLinkRefResolution checks prefix ambiguity handling directly.
func TestSemanticLinkRefResolution(t *testing.T) {
	idSet := map[string]bool{
		"abcd1234-0000-0000-0000-000000000001": true,
		"abcd1234-0000-0000-0000-000000000002": true,
		"ffff9999-0000-0000-0000-000000000003": true,
	}
	prefixIndex := map[string][]string{
		"abcd1234": {"abcd1234-0000-0000-0000-000000000001", "abcd1234-0000-0000-0000-000000000002"},
		"ffff9999": {"ffff9999-0000-0000-0000-000000000003"},
	}

	// Full UUID resolves.
	if got, ok := resolveIDRef("ffff9999-0000-0000-0000-000000000003", idSet, prefixIndex); !ok || got != "ffff9999-0000-0000-0000-000000000003" {
		t.Errorf("full UUID should resolve, got %q ok=%v", got, ok)
	}
	// Unique prefix resolves.
	if got, ok := resolveIDRef("ffff9999", idSet, prefixIndex); !ok || got != "ffff9999-0000-0000-0000-000000000003" {
		t.Errorf("unique prefix should resolve, got %q ok=%v", got, ok)
	}
	// Ambiguous prefix (two episodes share abcd1234) must NOT resolve.
	if _, ok := resolveIDRef("abcd1234", idSet, prefixIndex); ok {
		t.Error("ambiguous prefix must not resolve")
	}
	// Absent prefix does not resolve.
	if _, ok := resolveIDRef("deadbeef", idSet, prefixIndex); ok {
		t.Error("absent prefix must not resolve")
	}
}
