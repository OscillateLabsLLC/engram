package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/dreamer"
	"github.com/oscillatelabsllc/engram/internal/models"
)

// fakeLLM is a deterministic in-process LLM for dream endpoint tests
type fakeLLM struct {
	mu       sync.Mutex
	response json.RawMessage
	err      error
	calls    int
}

func (f *fakeLLM) ChatJSON(ctx context.Context, system, user, schema string) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func (f *fakeLLM) Model() string { return "fake-model" }

func setupDreamServer(t *testing.T, llm dreamer.LLM) (*Server, *db.Store) {
	t.Helper()
	store, err := db.NewStore(t.TempDir() + "/test.duckdb")
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	embedder := &fakeEmbedder{model: "fake-embed", dims: 768}
	var d *dreamer.Dreamer
	if llm != nil {
		d = dreamer.New(store, llm, embedder, 5*time.Second, nil, nil)
	}
	return NewServer(store, embedder, d, "0"), store
}

func waitForDream(t *testing.T, s *Server) DreamStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.dreamMu.Lock()
		status := s.dream
		s.dreamMu.Unlock()
		if !status.Running && status.FinishedAt != nil {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("dream job did not finish in time")
	return DreamStatus{}
}

func TestDreamEndpoint(t *testing.T) {
	t.Run("enriches unenriched episodes", func(t *testing.T) {
		llm := &fakeLLM{response: json.RawMessage(`{"triples":[{"subject":"Mike","predicate":"uses","object":"DuckDB","confidence":0.9}]}`)}
		s, store := setupDreamServer(t, llm)
		ctx := context.Background()

		for _, content := range []string{"Mike uses DuckDB", "Mike uses DuckDB daily"} {
			if err := store.InsertEpisode(ctx, &models.Episode{Content: content, Source: "test"}); err != nil {
				t.Fatalf("Failed to insert episode: %v", err)
			}
		}

		req := httptest.NewRequest("POST", "/api/v1/admin/dream", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
		}

		status := waitForDream(t, s)
		if status.Total != 2 {
			t.Errorf("Expected 2 targets, got %d", status.Total)
		}
		if status.Done != 2 || status.Failed != 0 {
			t.Errorf("Expected 2 done / 0 failed, got %d/%d", status.Done, status.Failed)
		}

		count, err := store.CountUnenrichedEpisodes(ctx, nil)
		if err != nil {
			t.Fatalf("CountUnenrichedEpisodes failed: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected all episodes enriched, %d remain", count)
		}
	})

	t.Run("counts per-episode failures", func(t *testing.T) {
		// LLM answers the probe but returns an unparseable extraction payload
		llm := &fakeLLM{response: json.RawMessage(`"not an envelope"`)}
		s, store := setupDreamServer(t, llm)
		ctx := context.Background()

		if err := store.InsertEpisode(ctx, &models.Episode{Content: "whatever", Source: "test"}); err != nil {
			t.Fatalf("Failed to insert episode: %v", err)
		}

		req := httptest.NewRequest("POST", "/api/v1/admin/dream", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
		}

		status := waitForDream(t, s)
		if status.Done != 1 || status.Failed != 1 {
			t.Errorf("Expected 1 done / 1 failed, got %d/%d", status.Done, status.Failed)
		}

		// Poison episode was stamped, not left to loop
		count, _ := store.CountUnenrichedEpisodes(ctx, nil)
		if count != 0 {
			t.Errorf("Expected poison episode stamped, %d remain", count)
		}
	})

	t.Run("returns 502 when LLM is unreachable", func(t *testing.T) {
		llm := &fakeLLM{err: errors.New("connection refused")}
		s, _ := setupDreamServer(t, llm)

		req := httptest.NewRequest("POST", "/api/v1/admin/dream", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadGateway {
			t.Fatalf("Expected 502, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("returns 503 when no LLM is configured", func(t *testing.T) {
		s, _ := setupDreamServer(t, nil)

		req := httptest.NewRequest("POST", "/api/v1/admin/dream", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("Expected 503 on POST, got %d: %s", w.Code, w.Body.String())
		}

		req = httptest.NewRequest("GET", "/api/v1/admin/dream", nil)
		w = httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("Expected 503 on GET, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("rejects concurrent jobs with 409", func(t *testing.T) {
		llm := &fakeLLM{response: json.RawMessage(`{"triples":[]}`)}
		s, _ := setupDreamServer(t, llm)

		s.dreamMu.Lock()
		s.dream = DreamStatus{Running: true}
		s.dreamMu.Unlock()

		req := httptest.NewRequest("POST", "/api/v1/admin/dream", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusConflict {
			t.Fatalf("Expected 409, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("status endpoint reports job and backlog", func(t *testing.T) {
		llm := &fakeLLM{response: json.RawMessage(`{"triples":[]}`)}
		s, store := setupDreamServer(t, llm)
		ctx := context.Background()

		if err := store.InsertEpisode(ctx, &models.Episode{Content: "pending", Source: "test"}); err != nil {
			t.Fatalf("Failed to insert episode: %v", err)
		}

		req := httptest.NewRequest("GET", "/api/v1/admin/dream", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var body struct {
			Job                DreamStatus `json:"job"`
			UnenrichedEpisodes int         `json:"unenriched_episodes"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}
		if body.UnenrichedEpisodes != 1 {
			t.Errorf("Expected 1 unenriched episode, got %d", body.UnenrichedEpisodes)
		}
		if body.Job.Running {
			t.Error("Expected no running job")
		}
	})
}

func TestTriggerDream(t *testing.T) {
	t.Run("starts a job like the admin endpoint", func(t *testing.T) {
		llm := &fakeLLM{response: json.RawMessage(`{"triples":[]}`)}
		s, store := setupDreamServer(t, llm)
		ctx := context.Background()

		if err := store.InsertEpisode(ctx, &models.Episode{Content: "tick", Source: "test"}); err != nil {
			t.Fatalf("Failed to insert episode: %v", err)
		}

		if err := s.TriggerDream(ctx); err != nil {
			t.Fatalf("TriggerDream failed: %v", err)
		}
		status := waitForDream(t, s)
		if status.Done != 1 {
			t.Errorf("Expected 1 done, got %d", status.Done)
		}
	})

	t.Run("reports ErrDreamRunning when a job is in flight", func(t *testing.T) {
		llm := &fakeLLM{response: json.RawMessage(`{"triples":[]}`)}
		s, store := setupDreamServer(t, llm)
		ctx := context.Background()

		if err := store.InsertEpisode(ctx, &models.Episode{Content: "tick", Source: "test"}); err != nil {
			t.Fatalf("Failed to insert episode: %v", err)
		}

		s.dreamMu.Lock()
		s.dream = DreamStatus{Running: true}
		s.dreamMu.Unlock()

		if err := s.TriggerDream(ctx); !errors.Is(err, ErrDreamRunning) {
			t.Errorf("Expected ErrDreamRunning, got %v", err)
		}
	})

	t.Run("skips silently when there is no backlog", func(t *testing.T) {
		llm := &fakeLLM{response: json.RawMessage(`{"triples":[]}`)}
		s, _ := setupDreamServer(t, llm)

		if err := s.TriggerDream(context.Background()); err != nil {
			t.Fatalf("TriggerDream with empty backlog should be a no-op, got %v", err)
		}
		s.dreamMu.Lock()
		running := s.dream.Running
		s.dreamMu.Unlock()
		if running {
			t.Error("No job should start when there is nothing to enrich")
		}
	})
}
