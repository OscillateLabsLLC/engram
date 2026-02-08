package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/oscillatelabsllc/engram/internal/api"
	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/embedding"
	"github.com/oscillatelabsllc/engram/internal/mcp"
)

func main() {
	// Parse command-line flags
	mode := flag.String("mode", "stdio", "Server mode: stdio or http")
	port := flag.String("port", "8080", "HTTP server port (only used in http mode)")
	flag.Parse()

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

	// Print startup info
	fmt.Fprintf(os.Stderr, "===================================\n")
	fmt.Fprintf(os.Stderr, "Engram memory system starting...\n")
	fmt.Fprintf(os.Stderr, "Mode: %s\n", *mode)
	fmt.Fprintf(os.Stderr, "Database: %s\n", dbPath)
	fmt.Fprintf(os.Stderr, "Ollama: %s\n", ollamaURL)
	fmt.Fprintf(os.Stderr, "Embedding model: %s\n", embeddingModel)
	if *mode == "http" {
		fmt.Fprintf(os.Stderr, "HTTP Port: %s\n", *port)
	}
	fmt.Fprintf(os.Stderr, "===================================\n")

	// Start server based on mode
	switch *mode {
	case "stdio":
		// Original MCP stdio mode
		server := mcp.NewServer(store, embedder)
		if err := server.Serve(); err != nil {
			log.Fatalf("Server error: %v", err)
		}

	case "http":
		// HTTP mode with both REST API and MCP SSE
		// Create MCP server for SSE transport
		mcpServer := mcp.NewServer(store, embedder)

		// Create API server with both REST and MCP SSE
		apiServer := api.NewServer(store, embedder, *port)
		apiServer.AddMCPServer(mcpServer.GetMCPServer())

		if err := apiServer.Serve(); err != nil {
			log.Fatalf("Server error: %v", err)
		}

	default:
		log.Fatalf("Invalid mode: %s (must be 'stdio' or 'http')", *mode)
	}
}
