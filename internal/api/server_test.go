package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/embedding"
)

func setupTestServer(t *testing.T) *Server {
	t.Helper()
	tmpFile := t.TempDir() + "/test.duckdb"
	store, err := db.NewStore(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	embedder := embedding.NewClient("http://localhost:11434", "nomic-embed-text")
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
