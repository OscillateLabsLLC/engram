package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/embedding"
	"github.com/oscillatelabsllc/engram/internal/models"
)

// Server implements the MCP server for Engram
type Server struct {
	store     *db.Store
	embedder  *embedding.Client
	mcpServer *server.MCPServer
}

// NewServer creates a new MCP server
func NewServer(store *db.Store, embedder *embedding.Client) *Server {
	s := &Server{
		store:    store,
		embedder: embedder,
	}

	// Create MCP server with tools
	s.mcpServer = server.NewMCPServer(
		"Engram Memory System",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// Register tools
	s.registerTools()

	return s
}

// registerTools registers all MCP tools
func (s *Server) registerTools() {
	// add_memory tool
	s.mcpServer.AddTool(mcp.Tool{
		Name:        "add_memory",
		Description: "Store a new episode in memory",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"content": map[string]interface{}{
					"type":        "string",
					"description": "The episode content to store",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Human-readable label for the episode",
				},
				"source": map[string]interface{}{
					"type":        "string",
					"description": "Identifier for what created this memory (e.g., 'claude-desktop', 'claude-code', 'my-app'). Use a consistent value per client so you can filter by it later.",
				},
				"source_model": map[string]interface{}{
					"type":        "string",
					"description": "Model that created this episode (e.g., 'opus-4.6')",
				},
				"source_description": map[string]interface{}{
					"type":        "string",
					"description": "Freeform context about the episode",
				},
				"group_id": map[string]interface{}{
					"type":        "string",
					"description": "Advanced: namespace for multi-tenant isolation. Omit this in almost all cases — the server assigns a default automatically. Only set if you are deliberately partitioning memories between separate users or contexts.",
				},
				"tags": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "string",
					},
					"description": "Tags for categorization",
				},
				"valid_at": map[string]interface{}{
					"type":        "string",
					"description": "When the information became true (ISO 8601)",
				},
				"metadata": map[string]interface{}{
					"type":        "string",
					"description": "JSON string with additional metadata",
				},
			},
			Required: []string{"content", "source"},
		},
	}, s.handleAddMemory)

	// search tool
	s.mcpServer.AddTool(mcp.Tool{
		Name:        "search",
		Description: "Search episodes using semantic similarity, temporal, and tag filters. For most searches, only provide 'query'. All other parameters are optional secondary filters — omit them unless you have a specific reason to narrow results.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Natural language text to search for. The system handles semantic matching automatically — just describe what you're looking for.",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results to return (default: 10)",
				},
				"before": map[string]interface{}{
					"type":        "string",
					"description": "Only return episodes created before this time (ISO 8601). Optional.",
				},
				"after": map[string]interface{}{
					"type":        "string",
					"description": "Only return episodes created after this time (ISO 8601). Optional.",
				},
				"tags": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "string",
					},
					"description": "Narrow results to episodes that have ALL of these tags. Only use if you know specific tags were stored. Optional.",
				},
				"source": map[string]interface{}{
					"type":        "string",
					"description": "Advanced filter: narrow results to a specific source client (e.g., 'claude-desktop'). Omit this in almost all cases — it will exclude memories from other sources. Only use if you need results from one specific client.",
				},
				"group_id": map[string]interface{}{
					"type":        "string",
					"description": "Advanced filter: narrow results to a specific group namespace. Omit this in almost all cases — it will exclude memories stored under other group IDs. Only use if you deliberately stored memories under a specific group.",
				},
				"include_expired": map[string]interface{}{
					"type":        "boolean",
					"description": "Include episodes that have been marked as expired (default: false). Optional.",
				},
			},
			Required: []string{},
		},
	}, s.handleSearch)

	// get_episodes tool
	s.mcpServer.AddTool(mcp.Tool{
		Name:        "get_episodes",
		Description: "Retrieve recent episodes in chronological order. All parameters are optional — call with no arguments to get the most recent episodes.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of episodes to return (default: 10)",
				},
				"before": map[string]interface{}{
					"type":        "string",
					"description": "Only return episodes created before this time (ISO 8601). Optional.",
				},
				"after": map[string]interface{}{
					"type":        "string",
					"description": "Only return episodes created after this time (ISO 8601). Optional.",
				},
				"group_id": map[string]interface{}{
					"type":        "string",
					"description": "Advanced filter: narrow results to a specific group namespace. Omit this in almost all cases — it will exclude memories stored under other group IDs. Only use if you deliberately stored memories under a specific group.",
				},
			},
			Required: []string{},
		},
	}, s.handleGetEpisodes)

	// update_episode tool
	s.mcpServer.AddTool(mcp.Tool{
		Name:        "update_episode",
		Description: "Update metadata, tags, or expiration of an episode",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "string",
					"description": "Episode ID to update",
				},
				"tags": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "string",
					},
					"description": "New tags array",
				},
				"expired_at": map[string]interface{}{
					"type":        "string",
					"description": "Expiration time (ISO 8601)",
				},
				"metadata": map[string]interface{}{
					"type":        "string",
					"description": "JSON string with metadata",
				},
			},
			Required: []string{"id"},
		},
	}, s.handleUpdateEpisode)

	// get_status tool
	s.mcpServer.AddTool(mcp.Tool{
		Name:        "get_status",
		Description: "Health check for the memory system",
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]interface{}{},
			Required:   []string{},
		},
	}, s.handleGetStatus)
}

// Tool handlers

// parseParams converts MCP request arguments to a struct
func parseParams(args interface{}, target interface{}) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func (s *Server) handleAddMemory(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		Content           string   `json:"content"`
		Name              string   `json:"name"`
		Source            string   `json:"source"`
		SourceModel       string   `json:"source_model"`
		SourceDescription string   `json:"source_description"`
		GroupID           string   `json:"group_id"`
		Tags              []string `json:"tags"`
		ValidAt           string   `json:"valid_at"`
		Metadata          string   `json:"metadata"`
	}

	if err := parseParams(request.Params.Arguments, &params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
	}

	// Generate embedding with a fresh context (5 second timeout)
	// Using background context to avoid cancellation from MCP request context
	embedCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	emb, err := s.embedder.Generate(embedCtx, params.Content)
	if err != nil {
		// Log error but continue with NULL embedding
		fmt.Fprintf(os.Stderr, "Warning: Failed to generate embedding: %v\n", err)
		emb = nil
	} else {
		fmt.Fprintf(os.Stderr, "Success: Generated embedding with %d dimensions\n", len(emb))
	}

	// Parse valid_at if provided
	var validAt *time.Time
	if params.ValidAt != "" {
		t, err := time.Parse(time.RFC3339, params.ValidAt)
		if err == nil {
			validAt = &t
		}
	}

	// Create episode
	ep := &models.Episode{
		Content:           params.Content,
		Name:              params.Name,
		Source:            params.Source,
		SourceModel:       params.SourceModel,
		SourceDescription: params.SourceDescription,
		GroupID:           params.GroupID,
		Tags:              params.Tags,
		Embedding:         emb,
		ValidAt:           validAt,
		Metadata:          params.Metadata,
	}

	if err := s.store.InsertEpisode(ctx, ep); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to store episode: %v", err)), nil
	}

	result, _ := json.Marshal(map[string]interface{}{
		"success": true,
		"id":      ep.ID,
		"message": "Episode stored successfully",
	})

	return mcp.NewToolResultText(string(result)), nil
}

func (s *Server) handleSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		Query          string   `json:"query"`
		GroupID        string   `json:"group_id"`
		MaxResults     int      `json:"max_results"`
		Before         string   `json:"before"`
		After          string   `json:"after"`
		Tags           []string `json:"tags"`
		Source         string   `json:"source"`
		IncludeExpired bool     `json:"include_expired"`
	}

	if err := parseParams(request.Params.Arguments, &params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
	}

	// Generate embedding for semantic search
	var queryEmbedding []float32
	if params.Query != "" {
		embedCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		emb, err := s.embedder.Generate(embedCtx, params.Query)
		if err != nil {
			// Log warning but continue without semantic search - will fall back to temporal ordering
			fmt.Fprintf(os.Stderr, "Warning: Failed to generate query embedding: %v\n", err)
		} else {
			queryEmbedding = emb
			fmt.Fprintf(os.Stderr, "Success: Generated query embedding with %d dimensions\n", len(emb))
		}
	}

	// Parse time filters
	var before, after *time.Time
	if params.Before != "" {
		t, err := time.Parse(time.RFC3339, params.Before)
		if err == nil {
			before = &t
		}
	}
	if params.After != "" {
		t, err := time.Parse(time.RFC3339, params.After)
		if err == nil {
			after = &t
		}
	}

	// Build search params
	searchParams := models.SearchParams{
		Query:          params.Query,
		QueryEmbedding: queryEmbedding,
		GroupID:        params.GroupID,
		MaxResults:     params.MaxResults,
		Before:         before,
		After:          after,
		Tags:           params.Tags,
		Source:         params.Source,
		IncludeExpired: params.IncludeExpired,
	}

	episodes, err := s.store.Search(ctx, searchParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	result, _ := json.Marshal(episodes)
	return mcp.NewToolResultText(string(result)), nil
}

func (s *Server) handleGetEpisodes(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		GroupID    string `json:"group_id"`
		MaxResults int    `json:"max_results"`
		Before     string `json:"before"`
		After      string `json:"after"`
	}

	if err := parseParams(request.Params.Arguments, &params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
	}

	// Parse time filters
	var before, after *time.Time
	if params.Before != "" {
		t, err := time.Parse(time.RFC3339, params.Before)
		if err == nil {
			before = &t
		}
	}
	if params.After != "" {
		t, err := time.Parse(time.RFC3339, params.After)
		if err == nil {
			after = &t
		}
	}

	searchParams := models.SearchParams{
		GroupID:    params.GroupID,
		MaxResults: params.MaxResults,
		Before:     before,
		After:      after,
	}

	episodes, err := s.store.Search(ctx, searchParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get episodes: %v", err)), nil
	}

	result, _ := json.Marshal(episodes)
	return mcp.NewToolResultText(string(result)), nil
}

func (s *Server) handleUpdateEpisode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		ID        string   `json:"id"`
		Tags      []string `json:"tags"`
		ExpiredAt string   `json:"expired_at"`
		Metadata  string   `json:"metadata"`
	}

	if err := parseParams(request.Params.Arguments, &params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
	}

	updateParams := models.UpdateParams{}

	if len(params.Tags) > 0 {
		updateParams.Tags = &params.Tags
	}

	if params.ExpiredAt != "" {
		t, err := time.Parse(time.RFC3339, params.ExpiredAt)
		if err == nil {
			updateParams.ExpiredAt = &t
		}
	}

	if params.Metadata != "" {
		updateParams.Metadata = &params.Metadata
	}

	if err := s.store.UpdateEpisode(ctx, params.ID, updateParams); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to update episode: %v", err)), nil
	}

	result, _ := json.Marshal(map[string]interface{}{
		"success": true,
		"message": "Episode updated successfully",
	})

	return mcp.NewToolResultText(string(result)), nil
}

func (s *Server) handleGetStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Simple health check
	result, _ := json.Marshal(map[string]interface{}{
		"status":  "healthy",
		"version": "1.0.0",
		"message": "Engram memory system is operational",
	})

	return mcp.NewToolResultText(string(result)), nil
}

// Serve starts the MCP server with stdio transport
func (s *Server) Serve() error {
	return server.ServeStdio(s.mcpServer)
}

// GetMCPServer returns the underlying MCP server for use with other transports (e.g., SSE)
func (s *Server) GetMCPServer() *server.MCPServer {
	return s.mcpServer
}
