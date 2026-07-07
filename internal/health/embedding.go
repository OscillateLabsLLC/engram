// Package health provides background dependency probes so degraded
// dependencies surface in status endpoints instead of failing silently.
package health

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// Embedder is the minimal embedding capability the prober exercises
type Embedder interface {
	Generate(ctx context.Context, text string) ([]float32, error)
	Model() string
}

// EmbeddingStatus is a point-in-time snapshot of embedding health,
// shaped for direct inclusion in status/health responses
type EmbeddingStatus struct {
	Status              string     `json:"status"` // "ok" | "degraded" | "unknown"
	Model               string     `json:"model"`
	Error               string     `json:"error,omitempty"`
	LatencyMS           int64      `json:"latency_ms,omitempty"`
	LastChecked         *time.Time `json:"last_checked,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures,omitempty"`
}

const probeText = "engram embedding health probe"

// EmbeddingProber periodically generates a test embedding and records the
// outcome. Vector search silently degrades to keyword-only when embedding
// calls fail, so a broken endpoint is invisible without an active probe.
type EmbeddingProber struct {
	embedder     Embedder
	interval     time.Duration
	timeout      time.Duration
	expectedDims int // 0 disables the dimension check

	mu     sync.Mutex
	status EmbeddingStatus
}

// NewEmbeddingProber creates a prober. interval <= 0 defaults to 5 minutes.
// expectedDims, when nonzero, marks mismatched vector sizes as degraded.
func NewEmbeddingProber(e Embedder, interval time.Duration, expectedDims int) *EmbeddingProber {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &EmbeddingProber{
		embedder:     e,
		interval:     interval,
		timeout:      15 * time.Second,
		expectedDims: expectedDims,
		status:       EmbeddingStatus{Status: "unknown", Model: e.Model()},
	}
}

// Start launches the probe loop: one immediate probe (so a dead endpoint is
// loud at startup), then one per interval until ctx is cancelled.
func (p *EmbeddingProber) Start(ctx context.Context) {
	go func() {
		p.probe(ctx)
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.probe(ctx)
			}
		}
	}()
}

// Status returns the latest snapshot
func (p *EmbeddingProber) Status() EmbeddingStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// probe runs a single embedding call and records the transition
func (p *EmbeddingProber) probe(ctx context.Context) {
	probeCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	start := time.Now()
	emb, err := p.embedder.Generate(probeCtx, probeText)
	latency := time.Since(start)
	if err == nil && p.expectedDims > 0 && len(emb) != p.expectedDims {
		err = fmt.Errorf("model %q produced %d-dimensional embedding, schema requires %d",
			p.embedder.Model(), len(emb), p.expectedDims)
	}
	if ctx.Err() != nil {
		return // shutdown, not a health signal
	}

	now := time.Now()
	p.mu.Lock()
	prev := p.status.Status
	p.status.Model = p.embedder.Model()
	p.status.LastChecked = &now
	if err != nil {
		p.status.Status = "degraded"
		p.status.Error = err.Error()
		p.status.LatencyMS = 0
		p.status.ConsecutiveFailures++
	} else {
		p.status.Status = "ok"
		p.status.Error = ""
		p.status.LatencyMS = latency.Milliseconds()
		p.status.ConsecutiveFailures = 0
	}
	next := p.status.Status
	failures := p.status.ConsecutiveFailures
	p.mu.Unlock()

	switch {
	case next == "degraded" && prev != "degraded":
		fmt.Fprintf(os.Stderr, "ERROR: embedding endpoint DEGRADED — vector search is falling back to keyword-only: %v\n", err)
	case next == "degraded":
		if failures%10 == 0 { // periodic reminder without log spam
			fmt.Fprintf(os.Stderr, "ERROR: embedding endpoint still degraded (%d consecutive failures): %v\n", failures, err)
		}
	case next == "ok" && prev == "degraded":
		fmt.Fprintf(os.Stderr, "Embedding endpoint recovered, vector search restored (probe latency %dms)\n", latency.Milliseconds())
	}
}
