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
)

// ErrDreamRunning is returned when a dream job is already in flight
var ErrDreamRunning = errors.New("a dream job is already running")

// dreamProbeTimeout bounds the LLM reachability check before starting a job
const dreamProbeTimeout = 15 * time.Second

// DreamStatus reports the state of the current or most recent dream job
type DreamStatus struct {
	Running    bool       `json:"running"`
	Total      int        `json:"total"`
	Done       int        `json:"done"`
	Failed     int        `json:"failed"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// handleStartDream launches an async dreamer pass over unenriched episodes.
// The LLM is probed first so a dead endpoint fails fast with 502 instead of
// stamping every episode with an enrichment error.
func (s *Server) handleStartDream(w http.ResponseWriter, r *http.Request) {
	if s.dreamer == nil {
		errorResponse(w, http.StatusServiceUnavailable, "no LLM configured")
		return
	}

	var req struct{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		errorResponse(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	probeCtx, cancel := context.WithTimeout(r.Context(), dreamProbeTimeout)
	defer cancel()
	if err := s.dreamer.Probe(probeCtx); err != nil {
		errorResponse(w, http.StatusBadGateway, "LLM endpoint unavailable: "+err.Error())
		return
	}

	count, err := s.store.CountUnenrichedEpisodes(r.Context(), s.dreamer.SkipTags())
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to count unenriched episodes: "+err.Error())
		return
	}

	if err := s.startDreamJob(count); err != nil {
		s.dreamMu.Lock()
		status := s.dream
		s.dreamMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
			"job":     status,
		})
		return
	}

	successResponse(w, map[string]interface{}{
		"success":    true,
		"message":    "dream started",
		"unenriched": count,
	})
}

// handleGetDream reports job progress plus the current enrichment backlog
func (s *Server) handleGetDream(w http.ResponseWriter, r *http.Request) {
	if s.dreamer == nil {
		errorResponse(w, http.StatusServiceUnavailable, "no LLM configured")
		return
	}

	s.dreamMu.Lock()
	status := s.dream
	s.dreamMu.Unlock()

	count, err := s.store.CountUnenrichedEpisodes(r.Context(), s.dreamer.SkipTags())
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to count unenriched episodes: "+err.Error())
		return
	}

	successResponse(w, map[string]interface{}{
		"job":                 status,
		"unenriched_episodes": count,
	})
}

// TriggerDream starts a dream job outside the HTTP path — used by the
// optional dream interval ticker. It probes the LLM, skips silently when the
// backlog is empty, and returns ErrDreamRunning when a job is in flight.
func (s *Server) TriggerDream(ctx context.Context) error {
	if s.dreamer == nil {
		return errors.New("no LLM configured")
	}

	probeCtx, cancel := context.WithTimeout(ctx, dreamProbeTimeout)
	defer cancel()
	if err := s.dreamer.Probe(probeCtx); err != nil {
		return fmt.Errorf("LLM endpoint unavailable: %w", err)
	}

	count, err := s.store.CountUnenrichedEpisodes(ctx, s.dreamer.SkipTags())
	if err != nil {
		return fmt.Errorf("failed to count unenriched episodes: %w", err)
	}
	if count == 0 {
		return nil
	}

	return s.startDreamJob(count)
}

// startDreamJob transitions job state to running and launches the async
// worker. Returns ErrDreamRunning when a job is already in flight.
func (s *Server) startDreamJob(total int) error {
	s.dreamMu.Lock()
	if s.dream.Running {
		s.dreamMu.Unlock()
		return ErrDreamRunning
	}

	now := time.Now()
	s.dream = DreamStatus{
		Running:   true,
		Total:     total,
		StartedAt: &now,
	}
	// Worker outlives the request; tie it to server lifetime instead
	workerCtx, workerCancel := context.WithCancel(context.Background())
	s.dreamCancel = workerCancel
	s.dreamMu.Unlock()

	go s.runDream(workerCtx)
	return nil
}

// runDream drives the dreamer crawl and tracks per-episode progress.
// Failures are counted, never fatal — failed episodes are stamped with their
// error so they are not retried.
func (s *Server) runDream(ctx context.Context) {
	err := s.dreamer.Process(ctx, func(failed bool) {
		s.dreamMu.Lock()
		s.dream.Done++
		if failed {
			s.dream.Failed++
		}
		s.dreamMu.Unlock()
	})

	now := time.Now()
	s.dreamMu.Lock()
	s.dream.Running = false
	s.dream.FinishedAt = &now
	if err != nil {
		s.dream.Error = err.Error()
	}
	final := s.dream
	s.dreamCancel = nil
	s.dreamMu.Unlock()

	fmt.Fprintf(os.Stderr, "Dream finished: %d/%d episodes enriched (%d failed)\n",
		final.Done-final.Failed, final.Total, final.Failed)
}

// stopDream cancels a running dream job, if any
func (s *Server) stopDream() {
	s.dreamMu.Lock()
	cancel := s.dreamCancel
	s.dreamMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
