package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/models"
)

// fakeEmbedder is a deterministic in-process Embedder for handler tests
type fakeEmbedder struct {
	mu    sync.Mutex
	model string
	dims  int
	err   error
	calls int
}

func (f *fakeEmbedder) Generate(ctx context.Context, text string) ([]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	emb := make([]float32, f.dims)
	if f.dims > 0 {
		emb[0] = float32(len(text))
	}
	return emb, nil
}

func (f *fakeEmbedder) Model() string { return f.model }

func setupReembedServer(t *testing.T, embedder Embedder) (*Server, *db.Store) {
	t.Helper()
	store, err := db.NewStore(t.TempDir() + "/test.duckdb")
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return NewServer(store, embedder, nil, "0"), store
}

func waitForReembed(t *testing.T, s *Server) ReembedStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.reembedMu.Lock()
		status := s.reembed
		s.reembedMu.Unlock()
		if !status.Running && status.FinishedAt != nil {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("re-embed job did not finish in time")
	return ReembedStatus{}
}

func TestReembedEndpoint(t *testing.T) {
	t.Run("re-embeds stale episodes and stamps new model", func(t *testing.T) {
		embedder := &fakeEmbedder{model: "new-model", dims: 768}
		s, store := setupReembedServer(t, embedder)
		ctx := context.Background()

		oldEmb := make([]float32, 768)
		oldEmb[0] = 1
		episodes := []*models.Episode{
			{Content: "stale one", Source: "test", Embedding: oldEmb, EmbeddingModel: "old-model"},
			{Content: "never embedded", Source: "test"},
			{Content: "already current", Source: "test", Embedding: oldEmb, EmbeddingModel: "new-model"},
		}
		for _, ep := range episodes {
			if err := store.InsertEpisode(ctx, ep); err != nil {
				t.Fatalf("Failed to insert episode: %v", err)
			}
		}

		req := httptest.NewRequest("POST", "/api/v1/admin/reembed", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
		}

		status := waitForReembed(t, s)
		if status.Total != 2 {
			t.Errorf("Expected 2 targets, got %d", status.Total)
		}
		if status.Done != 2 || status.Failed != 0 {
			t.Errorf("Expected 2 done / 0 failed, got %d/%d", status.Done, status.Failed)
		}

		counts, err := store.CountReembedTargets(ctx, "new-model", false)
		if err != nil {
			t.Fatalf("CountReembedTargets failed: %v", err)
		}
		if counts.Total() != 0 {
			t.Errorf("Expected no stale rows after re-embed, got %d", counts.Total())
		}
	})

	t.Run("force re-embeds current rows too", func(t *testing.T) {
		embedder := &fakeEmbedder{model: "new-model", dims: 768}
		s, store := setupReembedServer(t, embedder)
		ctx := context.Background()

		oldEmb := make([]float32, 768)
		oldEmb[0] = 1
		ep := &models.Episode{Content: "already current", Source: "test", Embedding: oldEmb, EmbeddingModel: "new-model"}
		if err := store.InsertEpisode(ctx, ep); err != nil {
			t.Fatalf("Failed to insert episode: %v", err)
		}

		req := httptest.NewRequest("POST", "/api/v1/admin/reembed", strings.NewReader(`{"force": true}`))
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
		}

		status := waitForReembed(t, s)
		if status.Total != 1 || status.Done != 1 {
			t.Errorf("Expected 1 target / 1 done with force, got %d/%d", status.Total, status.Done)
		}
	})

	t.Run("rejects wrong-dimension model before touching rows", func(t *testing.T) {
		embedder := &fakeEmbedder{model: "big-model", dims: 1536}
		s, _ := setupReembedServer(t, embedder)

		req := httptest.NewRequest("POST", "/api/v1/admin/reembed", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("Expected 400 for wrong dimensions, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "768") {
			t.Errorf("Error should mention required dimensions: %s", w.Body.String())
		}
	})

	t.Run("returns 502 when embedding endpoint is down", func(t *testing.T) {
		embedder := &fakeEmbedder{model: "new-model", dims: 768, err: context.DeadlineExceeded}
		s, _ := setupReembedServer(t, embedder)

		req := httptest.NewRequest("POST", "/api/v1/admin/reembed", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadGateway {
			t.Fatalf("Expected 502, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("rejects concurrent jobs with 409", func(t *testing.T) {
		embedder := &fakeEmbedder{model: "new-model", dims: 768}
		s, _ := setupReembedServer(t, embedder)

		// Simulate a running job
		s.reembedMu.Lock()
		s.reembed = ReembedStatus{Running: true}
		s.reembedMu.Unlock()

		req := httptest.NewRequest("POST", "/api/v1/admin/reembed", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusConflict {
			t.Fatalf("Expected 409, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("status endpoint reports staleness", func(t *testing.T) {
		embedder := &fakeEmbedder{model: "new-model", dims: 768}
		s, store := setupReembedServer(t, embedder)
		ctx := context.Background()

		if err := store.InsertEpisode(ctx, &models.Episode{Content: "no embedding", Source: "test"}); err != nil {
			t.Fatalf("Failed to insert episode: %v", err)
		}

		req := httptest.NewRequest("GET", "/api/v1/admin/reembed", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var body struct {
			Model           string `json:"model"`
			StaleEmbeddings struct {
				Episodes int `json:"episodes"`
			} `json:"stale_embeddings"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}
		if body.Model != "new-model" {
			t.Errorf("Expected model new-model, got %q", body.Model)
		}
		if body.StaleEmbeddings.Episodes != 1 {
			t.Errorf("Expected 1 stale episode, got %d", body.StaleEmbeddings.Episodes)
		}
	})
}
