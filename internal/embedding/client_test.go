package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGenerate(t *testing.T) {
	t.Run("successful embedding generation", func(t *testing.T) {
		expectedEmbedding := []float32{0.1, 0.2, 0.3, 0.4, 0.5}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify request
			if r.URL.Path != "/v1/embeddings" {
				t.Errorf("Expected path /v1/embeddings, got %s", r.URL.Path)
			}
			if r.Method != "POST" {
				t.Errorf("Expected POST, got %s", r.Method)
			}
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("Expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
			}

			// Verify request body
			var reqBody embedRequest
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				t.Fatalf("Failed to decode request body: %v", err)
			}
			if reqBody.Model != "test-model" {
				t.Errorf("Expected model test-model, got %s", reqBody.Model)
			}
			if reqBody.Input != "test text" {
				t.Errorf("Expected input 'test text', got %s", reqBody.Input)
			}

			// Send response
			resp := embedResponse{
				Data: []struct {
					Embedding []float32 `json:"embedding"`
				}{
					{Embedding: expectedEmbedding},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewClient(server.URL, "test-model")
		embedding, err := client.Generate(context.Background(), "test text")

		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(embedding) != len(expectedEmbedding) {
			t.Fatalf("Expected %d dimensions, got %d", len(expectedEmbedding), len(embedding))
		}
		for i, v := range embedding {
			if v != expectedEmbedding[i] {
				t.Errorf("Embedding[%d] = %f, expected %f", i, v, expectedEmbedding[i])
			}
		}
	})

	t.Run("handles non-200 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("model not found"))
		}))
		defer server.Close()

		client := NewClient(server.URL, "nonexistent-model")
		_, err := client.Generate(context.Background(), "test text")

		if err == nil {
			t.Fatal("Expected error for 500 response")
		}
		// Verify error message contains status and body
		if err.Error() != "embedding API returned status 500: model not found" {
			t.Errorf("Unexpected error message: %v", err)
		}
	})

	t.Run("handles empty embedding response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := embedResponse{Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewClient(server.URL, "test-model")
		_, err := client.Generate(context.Background(), "test text")

		if err == nil {
			t.Fatal("Expected error for empty embedding response")
		}
		if err.Error() != "no embeddings returned" {
			t.Errorf("Unexpected error message: %v", err)
		}
	})

	t.Run("handles malformed JSON response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("{invalid json"))
		}))
		defer server.Close()

		client := NewClient(server.URL, "test-model")
		_, err := client.Generate(context.Background(), "test text")

		if err == nil {
			t.Fatal("Expected error for malformed JSON")
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(5 * time.Second) // Simulate slow response
		}))
		defer server.Close()

		client := NewClient(server.URL, "test-model")
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err := client.Generate(ctx, "test text")

		if err == nil {
			t.Fatal("Expected error due to context timeout")
		}
	})

	t.Run("handles connection refused", func(t *testing.T) {
		// Use a port that's definitely not listening
		client := NewClient("http://localhost:59999", "test-model")
		_, err := client.Generate(context.Background(), "test text")

		if err == nil {
			t.Fatal("Expected error for connection refused")
		}
	})
}
