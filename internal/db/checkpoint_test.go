package db

import (
	"context"
	"os"
	"testing"

	"github.com/oscillatelabsllc/engram/internal/models"
)

// TestNoWALLeftBehind guards against a bricking scenario: an unflushed WAL
// containing startup migration DDL can fail DuckDB replay on the next open
// (internal error binding CURRENT_TIMESTAMP defaults during ReplayAlter).
// Initialization and Close must both checkpoint so the WAL never carries DDL.
func TestNoWALLeftBehind(t *testing.T) {
	dbPath := t.TempDir() + "/test.duckdb"
	walPath := dbPath + ".wal"

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	t.Run("initialize checkpoints startup DDL", func(t *testing.T) {
		if info, err := os.Stat(walPath); err == nil && info.Size() > 0 {
			t.Errorf("WAL should be empty after initialization, has %d bytes", info.Size())
		}
	})

	if err := store.InsertEpisode(context.Background(), &models.Episode{
		Content: "wal test", Source: "test",
	}); err != nil {
		t.Fatalf("Failed to insert episode: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Failed to close store: %v", err)
	}

	t.Run("close checkpoints writes", func(t *testing.T) {
		if info, err := os.Stat(walPath); err == nil && info.Size() > 0 {
			t.Errorf("WAL should be empty after Close, has %d bytes", info.Size())
		}
	})

	t.Run("database reopens with data intact", func(t *testing.T) {
		reopened, err := NewStore(dbPath)
		if err != nil {
			t.Fatalf("Failed to reopen database: %v", err)
		}
		defer reopened.Close()
		count, err := reopened.CountEpisodes(context.Background())
		if err != nil {
			t.Fatalf("Failed to count episodes: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 episode after reopen, got %d", count)
		}
	})
}
