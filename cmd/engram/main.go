package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/embedding"
	"github.com/oscillatelabsllc/engram/internal/mcp"
)

func main() {
	// Get configuration from environment
	dbPath := os.Getenv("DUCKDB_PATH")
	if dbPath == "" {
		// Default to current directory
		dbPath = filepath.Join(".", "engram.duckdb")
	}

	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	embeddingModel := os.Getenv("EMBEDDING_MODEL")
	if embeddingModel == "" {
		embeddingModel = "nomic-embed-text"
	}

	// Initialize database
	store, err := db.NewStore(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer store.Close()

	// Initialize embedding client
	embedder := embedding.NewClient(ollamaURL, embeddingModel)

	// Create and start MCP server
	server := mcp.NewServer(store, embedder)

	fmt.Fprintf(os.Stderr, "===================================\n")
	fmt.Fprintf(os.Stderr, "Engram memory system starting...\n")
	fmt.Fprintf(os.Stderr, "Database: %s\n", dbPath)
	fmt.Fprintf(os.Stderr, "Ollama: %s\n", ollamaURL)
	fmt.Fprintf(os.Stderr, "Embedding model: %s\n", embeddingModel)
	fmt.Fprintf(os.Stderr, "===================================\n")

	if err := server.Serve(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
