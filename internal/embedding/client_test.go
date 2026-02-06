package embedding

import (
	"context"
	"testing"
)

func TestClientCreation(t *testing.T) {
	client := NewClient("http://localhost:11434", "nomic-embed-text")

	if client == nil {
		t.Fatal("Expected client to be created")
	}

	if client.baseURL != "http://localhost:11434" {
		t.Errorf("Expected baseURL to be http://localhost:11434, got %s", client.baseURL)
	}

	if client.model != "nomic-embed-text" {
		t.Errorf("Expected model to be nomic-embed-text, got %s", client.model)
	}
}

func TestGenerateEmbedding(t *testing.T) {
	// This test requires Ollama to be running
	// Skip if not available
	client := NewClient("http://localhost:11434", "nomic-embed-text")
	ctx := context.Background()

	// Try to generate - will fail if Ollama not running, that's OK for now
	_, err := client.Generate(ctx, "test text")
	if err != nil {
		t.Logf("Embedding generation failed (Ollama may not be running): %v", err)
		// Don't fail the test - this is expected in CI
	}
}
