package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/mark3labs/mcp-go/server"
	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/embedding"
	"github.com/oscillatelabsllc/engram/internal/models"
)

// Server implements the HTTP API server for Engram
type Server struct {
	store      *db.Store
	embedder   *embedding.Client
	router     *chi.Mux
	port       string
	sseServer  *server.SSEServer
	mcpServer  *server.MCPServer
}

// NewServer creates a new HTTP API server
func NewServer(store *db.Store, embedder *embedding.Client, port string) *Server {
	s := &Server{
		store:    store,
		embedder: embedder,
		port:     port,
	}

	s.setupRouter()
	return s
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
	})

	s.router = r
}

// Serve starts the HTTP server
func (s *Server) Serve() error {
	addr := fmt.Sprintf(":%s", s.port)
	fmt.Printf("Starting HTTP server on %s\n", addr)
	fmt.Printf("OpenAPI spec available at http://localhost%s/openapi.json\n", addr)
	fmt.Printf("Health check at http://localhost%s/health\n", addr)
	return http.ListenAndServe(addr, s.router)
}

// handleHealth returns 200 OK if server is running
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
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
