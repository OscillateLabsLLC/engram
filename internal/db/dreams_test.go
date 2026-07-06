package db

import (
	"context"
	"testing"

	"github.com/oscillatelabsllc/engram/internal/models"
)

// seedDreamTriple inserts a quarantined triple re-extracted from n distinct
// episodes, returning its ID
func seedDreamTriple(t *testing.T, store *Store, subject, object string, n int) string {
	t.Helper()
	ctx := context.Background()
	subj, err := store.InsertEntity(ctx, &models.Entity{CanonicalName: subject}, 0.88)
	if err != nil {
		t.Fatalf("InsertEntity failed: %v", err)
	}
	obj, err := store.InsertEntity(ctx, &models.Entity{CanonicalName: object}, 0.88)
	if err != nil {
		t.Fatalf("InsertEntity failed: %v", err)
	}

	var id string
	for i := 0; i < n; i++ {
		ep := &models.Episode{Content: "episode content", Source: "test"}
		if err := store.InsertEpisode(ctx, ep); err != nil {
			t.Fatalf("InsertEpisode failed: %v", err)
		}
		tr := &models.KnowledgeTriple{
			SubjectEntityID: subj.ID,
			ObjectEntityID:  obj.ID,
			Predicate:       "related_to",
			Source:          "dreamer/test",
			SourceEpisodeID: ep.ID,
			Confidence:      0.8,
			Grounded:        false,
		}
		if err := store.InsertKnowledgeTriple(ctx, tr); err != nil {
			t.Fatalf("InsertKnowledgeTriple failed: %v", err)
		}
		if i == 0 {
			id = tr.ID
		}
	}
	return id
}

func TestRecurringDreams(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	ctx := context.Background()

	recurring := seedDreamTriple(t, store, "SubjectA", "ObjectA", 4) // recurrence 4
	seedDreamTriple(t, store, "SubjectB", "ObjectB", 1)              // recurrence 1 — below threshold

	t.Run("lists only triples at or above the threshold", func(t *testing.T) {
		dreams, err := store.ListRecurringDreams(ctx, "", 3, 10)
		if err != nil {
			t.Fatalf("ListRecurringDreams failed: %v", err)
		}
		if len(dreams) != 1 {
			t.Fatalf("Expected 1 recurring dream, got %d", len(dreams))
		}
		if dreams[0].ID != recurring {
			t.Errorf("Wrong triple surfaced")
		}
		if dreams[0].Recurrence != 4 {
			t.Errorf("Expected recurrence 4, got %d", dreams[0].Recurrence)
		}
		if dreams[0].SubjectName == "" || dreams[0].ObjectName == "" {
			t.Error("Names must be joined for display")
		}
	})

	t.Run("count matches", func(t *testing.T) {
		n, err := store.CountRecurringDreams(ctx, 3)
		if err != nil {
			t.Fatalf("CountRecurringDreams failed: %v", err)
		}
		if n != 1 {
			t.Errorf("Expected count 1, got %d", n)
		}
	})

	t.Run("appears in loose ends", func(t *testing.T) {
		le, err := store.FindLooseEnds(ctx, "", 10)
		if err != nil {
			t.Fatalf("FindLooseEnds failed: %v", err)
		}
		if len(le.RecurringDreams) != 1 {
			t.Errorf("Expected 1 recurring dream in loose ends, got %d", len(le.RecurringDreams))
		}
	})

	t.Run("confirm promotes to grounded and verified", func(t *testing.T) {
		if err := store.ResolveKnowledge(ctx, recurring, true); err != nil {
			t.Fatalf("ResolveKnowledge confirm failed: %v", err)
		}
		dreams, _ := store.ListRecurringDreams(ctx, "", 3, 10)
		if len(dreams) != 0 {
			t.Errorf("Confirmed triple must leave the quarantine, %d remain", len(dreams))
		}
		var grounded, verified bool
		if err := store.db.QueryRow("SELECT grounded, verified FROM knowledge WHERE id = ?", recurring).Scan(&grounded, &verified); err != nil {
			t.Fatalf("Failed to read resolved triple: %v", err)
		}
		if !grounded || !verified {
			t.Errorf("Confirmed triple must be grounded and verified, got grounded=%v verified=%v", grounded, verified)
		}
	})

	t.Run("reject expires without deleting", func(t *testing.T) {
		rejected := seedDreamTriple(t, store, "SubjectC", "ObjectC", 5)
		if err := store.ResolveKnowledge(ctx, rejected, false); err != nil {
			t.Fatalf("ResolveKnowledge reject failed: %v", err)
		}
		dreams, _ := store.ListRecurringDreams(ctx, "", 3, 10)
		for _, d := range dreams {
			if d.ID == rejected {
				t.Error("Rejected triple must not be surfaced again")
			}
		}
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM knowledge WHERE id = ?", rejected).Scan(&count); err != nil || count != 1 {
			t.Errorf("Rejected triple must remain in the store (demoted, not deleted), count=%d err=%v", count, err)
		}
	})

	t.Run("errors on unknown id", func(t *testing.T) {
		if err := store.ResolveKnowledge(ctx, "no-such-id", true); err == nil {
			t.Error("Expected error for unknown triple id")
		}
	})
}
