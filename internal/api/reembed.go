package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/oscillatelabsllc/engram/internal/db"
)

// embeddingDimensions is the vector size Engram's schema stores (FLOAT[768])
const embeddingDimensions = 768

// reembedBatchSize is how many rows are fetched per keyset page
const reembedBatchSize = 64

// ReembedStatus reports the state of the current or most recent re-embed job
type ReembedStatus struct {
	Running    bool       `json:"running"`
	Force      bool       `json:"force,omitempty"`
	Model      string     `json:"model,omitempty"`
	Total      int        `json:"total"`
	Done       int        `json:"done"`
	Failed     int        `json:"failed"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// handleStartReembed launches an async re-embed pass over episodes. Default
// mode regenerates only stale rows (missing embedding or stamped with a
// different model); {"force": true} regenerates everything.
func (s *Server) handleStartReembed(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Force bool `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		errorResponse(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	model := s.embedder.Model()

	// Probe before touching any rows: fail fast if the endpoint is down or
	// the configured model produces the wrong dimensionality
	probeCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	probe, err := s.embedder.Generate(probeCtx, "engram re-embed dimension probe")
	if err != nil {
		errorResponse(w, http.StatusBadGateway, "embedding endpoint unavailable: "+err.Error())
		return
	}
	if len(probe) != embeddingDimensions {
		errorResponse(w, http.StatusBadRequest, fmt.Sprintf(
			"model %q produces %d-dimensional embeddings but the schema requires %d — choose a %d-dim model",
			model, len(probe), embeddingDimensions, embeddingDimensions))
		return
	}

	counts, err := s.store.CountReembedTargets(r.Context(), model, req.Force)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to count re-embed targets: "+err.Error())
		return
	}

	s.reembedMu.Lock()
	if s.reembed.Running {
		status := s.reembed
		s.reembedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "a re-embed job is already running",
			"job":     status,
		})
		return
	}

	now := time.Now()
	s.reembed = ReembedStatus{
		Running:   true,
		Force:     req.Force,
		Model:     model,
		Total:     counts.Total(),
		StartedAt: &now,
	}
	// Worker outlives the request; tie it to server lifetime instead
	workerCtx, workerCancel := context.WithCancel(context.Background())
	s.reembedCancel = workerCancel
	s.reembedMu.Unlock()

	go s.runReembed(workerCtx, model, req.Force)

	successResponse(w, map[string]interface{}{
		"success": true,
		"message": "re-embed started",
		"targets": counts,
		"model":   model,
		"force":   req.Force,
	})
}

// handleGetReembed reports job progress plus current staleness counts
func (s *Server) handleGetReembed(w http.ResponseWriter, r *http.Request) {
	s.reembedMu.Lock()
	status := s.reembed
	s.reembedMu.Unlock()

	counts, err := s.store.CountReembedTargets(r.Context(), s.embedder.Model(), false)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to count stale embeddings: "+err.Error())
		return
	}

	successResponse(w, map[string]interface{}{
		"job":              status,
		"model":            s.embedder.Model(),
		"stale_embeddings": counts,
	})
}

// runReembed walks the episodes table and regenerates vectors. Failures are
// counted and skipped — keyset pagination guarantees forward progress, and a
// later run retries anything still stale.
func (s *Server) runReembed(ctx context.Context, model string, force bool) {
	tables := []struct {
		name   string
		list   func(ctx context.Context, afterID string, limit int) ([]db.ReembedItem, error)
		update func(ctx context.Context, id string, emb []float32) error
	}{
		{
			name: "episodes",
			list: func(ctx context.Context, afterID string, limit int) ([]db.ReembedItem, error) {
				return s.store.ListEpisodesForReembed(ctx, model, afterID, limit, force)
			},
			update: func(ctx context.Context, id string, emb []float32) error {
				return s.store.UpdateEpisodeEmbedding(ctx, id, emb, model)
			},
		},
	}

	var jobErr error

tableLoop:
	for _, table := range tables {
		afterID := ""
		for {
			items, err := table.list(ctx, afterID, reembedBatchSize)
			if err != nil {
				jobErr = fmt.Errorf("listing %s: %w", table.name, err)
				break tableLoop
			}
			if len(items) == 0 {
				break
			}

			for _, item := range items {
				if ctx.Err() != nil {
					jobErr = ctx.Err()
					break tableLoop
				}
				afterID = item.ID

				embedCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				emb, err := s.embedder.Generate(embedCtx, item.Text)
				cancel()

				ok := err == nil && len(emb) == embeddingDimensions
				if ok {
					if err := table.update(ctx, item.ID, emb); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: re-embed update failed for %s %s: %v\n", table.name, item.ID, err)
						ok = false
					}
				} else if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: re-embed generation failed for %s %s: %v\n", table.name, item.ID, err)
				}

				s.reembedMu.Lock()
				s.reembed.Done++
				if !ok {
					s.reembed.Failed++
				}
				s.reembedMu.Unlock()
			}
		}
	}

	now := time.Now()
	s.reembedMu.Lock()
	s.reembed.Running = false
	s.reembed.FinishedAt = &now
	if jobErr != nil {
		s.reembed.Error = jobErr.Error()
	}
	final := s.reembed
	s.reembedCancel = nil
	s.reembedMu.Unlock()

	fmt.Fprintf(os.Stderr, "Re-embed finished: %d/%d rows updated (%d failed) with model %s\n",
		final.Done-final.Failed, final.Total, final.Failed, model)
}

// stopReembed cancels a running re-embed job, if any
func (s *Server) stopReembed() {
	s.reembedMu.Lock()
	cancel := s.reembedCancel
	s.reembedMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
