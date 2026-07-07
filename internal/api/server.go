package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/mark3labs/mcp-go/server"
	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/health"
	"github.com/oscillatelabsllc/engram/internal/models"
)

// Embedder generates vector embeddings for text
type Embedder interface {
	Generate(ctx context.Context, text string) ([]float32, error)
	// Model returns the embedding model name, used to stamp provenance
	Model() string
}

// EmbeddingHealth reports the latest embedding-endpoint probe result
type EmbeddingHealth interface {
	Status() health.EmbeddingStatus
}

// Server implements the HTTP API server for Engram
type Server struct {
	store           *db.Store
	embedder        Embedder
	embeddingHealth EmbeddingHealth
	router          *chi.Mux
	port            string
	httpServer      *http.Server
	sseServer       *server.SSEServer
	mcpServer       *server.MCPServer

	// Re-embed job state (see reembed.go)
	reembedMu     sync.Mutex
	reembed       ReembedStatus
	reembedCancel context.CancelFunc
}

// NewServer creates a new HTTP API server
func NewServer(store *db.Store, embedder Embedder, port string) *Server {
	s := &Server{
		store:    store,
		embedder: embedder,
		port:     port,
	}

	s.setupRouter()
	return s
}

// SetEmbeddingHealth attaches a background embedding prober whose snapshot is
// reported by /health and /status. Optional: without it, embedding health is
// simply omitted from responses.
func (s *Server) SetEmbeddingHealth(h EmbeddingHealth) {
	s.embeddingHealth = h
}

// setupRouter configures all HTTP routes
func (s *Server) setupRouter() {
	r := chi.NewRouter()

	// Global middleware (no timeout here - we'll add it selectively)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// CORS for Open WebUI
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// Health check for Kubernetes (no timeout needed)
	r.Get("/health", s.handleHealth)
	r.Get("/ready", s.handleReady)

	// OpenAPI spec (no timeout needed)
	r.Get("/openapi.json", s.handleOpenAPISpec)

	// MCP SSE endpoint (will be added after server is created)
	// NO TIMEOUT MIDDLEWARE - SSE connections must stay open indefinitely
	// This gets mounted dynamically via AddMCPServer

	// API routes WITH timeout middleware (these are short-lived REST requests)
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.Timeout(60 * time.Second)) // Only apply timeout to API routes

		// Memory operations
		r.Post("/memory", s.handleAddMemory)
		r.Get("/memory/search", s.handleSearch)
		r.Get("/memory/episodes", s.handleGetEpisodes)
		r.Put("/memory/episodes/{id}", s.handleUpdateEpisode)
		r.Get("/status", s.handleGetStatus)

		// Admin operations
		r.Post("/admin/reembed", s.handleStartReembed)
		r.Get("/admin/reembed", s.handleGetReembed)
	})

	s.router = r
}

// Serve starts the HTTP server. It blocks until the server is shut down.
// Returns nil on clean shutdown via Shutdown(), or an error on failure.
func (s *Server) Serve() error {
	addr := fmt.Sprintf(":%s", s.port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.router,
	}
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.stopReembed()
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

// handleHealth returns 200 OK if server is running. Embedding health is
// reported in the body but never changes the status code: a broken embedding
// endpoint degrades search to keyword-only, and restarting the pod (what a
// failing liveness probe triggers) cannot fix an external dependency.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{"status": "healthy"}
	if s.embeddingHealth != nil {
		resp["embedding"] = s.embeddingHealth.Status()
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// handleReady checks if dependencies (DB, embedder) are ready
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	// Check DB connection
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Simple query to verify DB is accessible
	_, err := s.store.Search(ctx, models.SearchParams{
		MaxResults: 1,
		GroupID:    "health-check",
	})

	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "not ready",
			"error":  err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

// errorResponse writes a JSON error response
func errorResponse(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// successResponse writes a JSON success response
func successResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(data)
}

// AddMCPServer adds MCP SSE transport to the HTTP server
func (s *Server) AddMCPServer(mcpServer *server.MCPServer) {
	s.mcpServer = mcpServer

	// Create SSE server with base path and keep-alive enabled
	s.sseServer = server.NewSSEServer(
		mcpServer,
		server.WithBasePath("/mcp"),
		server.WithSSEEndpoint("/sse"),
		server.WithMessageEndpoint("/message"),
		server.WithKeepAlive(true),
		server.WithKeepAliveInterval(15*time.Second), // Send keep-alive every 15s
	)

	// Mount SSE server handler at the base path - it handles subrouting internally
	s.router.Mount("/mcp", s.sseServer)

	fmt.Fprintf(os.Stderr, "MCP SSE endpoint available at /mcp/sse\n")
	fmt.Fprintf(os.Stderr, "MCP Message endpoint available at /mcp/message\n")
	fmt.Fprintf(os.Stderr, "SSE keep-alive enabled (15s interval)\n")
}
