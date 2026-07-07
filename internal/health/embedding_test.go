package health

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeEmbedder struct {
	emb []float32
	err error
}

func (f *fakeEmbedder) Generate(ctx context.Context, text string) ([]float32, error) {
	return f.emb, f.err
}

func (f *fakeEmbedder) Model() string { return "fake-model" }

func TestEmbeddingProber(t *testing.T) {
	ctx := context.Background()

	t.Run("starts unknown", func(t *testing.T) {
		p := NewEmbeddingProber(&fakeEmbedder{}, time.Minute, 0)
		if got := p.Status().Status; got != "unknown" {
			t.Errorf("Expected unknown before first probe, got %q", got)
		}
	})

	t.Run("successful probe reports ok", func(t *testing.T) {
		p := NewEmbeddingProber(&fakeEmbedder{emb: make([]float32, 768)}, time.Minute, 768)
		p.probe(ctx)
		s := p.Status()
		if s.Status != "ok" {
			t.Errorf("Expected ok, got %q (error %q)", s.Status, s.Error)
		}
		if s.LastChecked == nil {
			t.Error("Expected LastChecked to be set")
		}
		if s.Model != "fake-model" {
			t.Errorf("Expected model stamp, got %q", s.Model)
		}
	})

	t.Run("failing probe reports degraded and counts failures", func(t *testing.T) {
		p := NewEmbeddingProber(&fakeEmbedder{err: errors.New("connection refused")}, time.Minute, 0)
		p.probe(ctx)
		p.probe(ctx)
		s := p.Status()
		if s.Status != "degraded" {
			t.Errorf("Expected degraded, got %q", s.Status)
		}
		if s.ConsecutiveFailures != 2 {
			t.Errorf("Expected 2 consecutive failures, got %d", s.ConsecutiveFailures)
		}
		if s.Error == "" {
			t.Error("Expected error message to be recorded")
		}
	})

	t.Run("wrong dimensionality is degraded", func(t *testing.T) {
		p := NewEmbeddingProber(&fakeEmbedder{emb: make([]float32, 384)}, time.Minute, 768)
		p.probe(ctx)
		if s := p.Status(); s.Status != "degraded" {
			t.Errorf("Expected degraded on dimension mismatch, got %q", s.Status)
		}
	})

	t.Run("recovery clears error and failure count", func(t *testing.T) {
		f := &fakeEmbedder{err: errors.New("boom")}
		p := NewEmbeddingProber(f, time.Minute, 0)
		p.probe(ctx)
		f.err = nil
		f.emb = make([]float32, 768)
		p.probe(ctx)
		s := p.Status()
		if s.Status != "ok" || s.Error != "" || s.ConsecutiveFailures != 0 {
			t.Errorf("Expected clean recovery, got %+v", s)
		}
	})

	t.Run("cancelled context does not overwrite status", func(t *testing.T) {
		p := NewEmbeddingProber(&fakeEmbedder{emb: make([]float32, 768)}, time.Minute, 0)
		p.probe(ctx)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		p.probe(cancelled)
		if s := p.Status(); s.Status != "ok" {
			t.Errorf("Shutdown-time probe should not mark degraded, got %q", s.Status)
		}
	})
}
