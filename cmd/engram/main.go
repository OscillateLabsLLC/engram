package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/oscillatelabsllc/engram/internal/api"
	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/embedding"
	"github.com/oscillatelabsllc/engram/internal/mcp"
	"github.com/oscillatelabsllc/engram/internal/proxy"
)

func main() {
	subcmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		subcmd = args[0]
		args = args[1:]
	}

	switch subcmd {
	case "serve":
		runServe(args)
	case "stdio":
		runStdio()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: engram [serve|stdio]\n")
		fmt.Fprintf(os.Stderr, "  serve   Start the HTTP/SSE server (default)\n")
		fmt.Fprintf(os.Stderr, "  stdio   Stdio proxy to a running server\n")
		os.Exit(1)
	}
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.String("port", "", "HTTP server port (default: 3490, env: ENGRAM_PORT)")
	fs.Parse(args)

	resolvedPort := "3490"
	if envPort := os.Getenv("ENGRAM_PORT"); envPort != "" {
		resolvedPort = envPort
	}
	if *port != "" {
		resolvedPort = *port
	}

	dbPath := os.Getenv("DUCKDB_PATH")
	if dbPath == "" {
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

	store, err := db.NewStore(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	embedder := embedding.NewClient(ollamaURL, embeddingModel)

	fmt.Fprintf(os.Stderr, "===================================\n")
	fmt.Fprintf(os.Stderr, "Engram memory system starting...\n")
	fmt.Fprintf(os.Stderr, "Mode: serve\n")
	fmt.Fprintf(os.Stderr, "Database: %s\n", dbPath)
	fmt.Fprintf(os.Stderr, "Ollama: %s\n", ollamaURL)
	fmt.Fprintf(os.Stderr, "Embedding model: %s\n", embeddingModel)
	fmt.Fprintf(os.Stderr, "Port: %s\n", resolvedPort)
	fmt.Fprintf(os.Stderr, "===================================\n")
	fmt.Fprintf(os.Stderr, "\nMCP SSE endpoint: http://localhost:%s/mcp/sse\n", resolvedPort)
	fmt.Fprintf(os.Stderr, "Health check:     http://localhost:%s/health\n\n", resolvedPort)

	mcpServer := mcp.NewServer(store, embedder)
	apiServer := api.NewServer(store, embedder, resolvedPort)
	apiServer.AddMCPServer(mcpServer.GetMCPServer())

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-ctx.Done()
		fmt.Fprintf(os.Stderr, "\nShutting down...\n")
		if err := apiServer.Shutdown(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "Shutdown error: %v\n", err)
		}
		store.Close()
	}()

	if err := apiServer.Serve(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func runStdio() {
	serverURL := os.Getenv("ENGRAM_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:3490"
	}

	fmt.Fprintf(os.Stderr, "Engram stdio proxy connecting to %s...\n", serverURL)

	if err := proxy.RunStdioProxy(serverURL); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

