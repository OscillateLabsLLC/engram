package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/embedding"
	"github.com/oscillatelabsllc/engram/internal/health"
)

func setupTestServer(t *testing.T) *Server {
	t.Helper()
	tmpFile := t.TempDir() + "/test.duckdb"
	store, err := db.NewStore(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	embedder := embedding.NewClient("http://localhost:11434", "nomic-embed-text", "")
	return NewServer(store, embedder, "0")
}

func TestHealthEndpoint(t *testing.T) {
	s := setupTestServer(t)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if body["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got %q", body["status"])
	}
}

func TestReadyEndpoint(t *testing.T) {
	s := setupTestServer(t)

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if body["status"] != "ready" {
		t.Errorf("Expected status 'ready', got %q", body["status"])
	}
}

func TestOpenAPIEndpoint(t *testing.T) {
	s := setupTestServer(t)

	req := httptest.NewRequest("GET", "/openapi.json", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode OpenAPI response: %v", err)
	}
	if body["openapi"] == nil {
		t.Error("Expected 'openapi' field in response")
	}
}

func TestShutdown(t *testing.T) {
	s := setupTestServer(t)

	t.Run("shutdown before serve is a no-op", func(t *testing.T) {
		err := s.Shutdown(context.Background())
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
	})
}

type stubEmbeddingHealth struct{ status health.EmbeddingStatus }

func (s *stubEmbeddingHealth) Status() health.EmbeddingStatus { return s.status }

func TestHealthEndpointReportsEmbeddingStatus(t *testing.T) {
	s := setupTestServer(t)
	s.SetEmbeddingHealth(&stubEmbeddingHealth{status: health.EmbeddingStatus{
		Status: "degraded", Model: "nomic-embed-text", Error: "connection refused",
	}})

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	// Liveness must stay 200 — embedding is an external dependency and a
	// pod restart cannot fix it
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 even when degraded, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	emb, ok := body["embedding"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected embedding block in health response, got %v", body)
	}
	if emb["status"] != "degraded" || emb["error"] != "connection refused" {
		t.Errorf("Expected degraded embedding status with error, got %v", emb)
	}
}

func TestStatusEndpointDegradesOnEmbeddingFailure(t *testing.T) {
	s := setupTestServer(t)
	s.SetEmbeddingHealth(&stubEmbeddingHealth{status: health.EmbeddingStatus{
		Status: "degraded", Model: "nomic-embed-text", Error: "boom",
	}})

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if body["status"] != "degraded" {
		t.Errorf("Expected top-level status degraded, got %v", body["status"])
	}
}
