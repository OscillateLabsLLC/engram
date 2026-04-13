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
		Description: "Search episodes using semantic similarity, keyword matching, or hybrid mode. For most searches, only provide 'query'. All other parameters are optional secondary filters — omit them unless you have a specific reason to narrow results.\n\nSearch mode guidance:\n- hybrid (recommended): best for most queries — balances semantic understanding with exact term matching.\n- vector: best for concept/intent queries where your words won't match the stored text (e.g. \"deployment preferences\" finding CI/CD memories).\n- keyword: best for exact terms, proper nouns, error codes, or version strings where semantic drift would hurt (e.g. \"mlx_lm.server\").\n\nNote: the default search_mode will change from 'vector' to 'hybrid' in the next major version.",
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
				"min_similarity": map[string]interface{}{
					"type":        "number",
					"description": "Minimum cosine similarity threshold (0.0-1.0). 0.5 is a reasonable floor to filter noise. Only applies in vector/hybrid mode. Optional.",
				},
				"search_mode": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"vector", "keyword", "hybrid"},
					"description": "How to search: 'vector' (default) finds by meaning, 'keyword' finds by exact words (BM25), 'hybrid' combines both. Use keyword for proper nouns/error codes, vector for concept queries, hybrid for everything else.",
				},
				"search_alpha": map[string]interface{}{
					"type":        "number",
					"description": "Hybrid mode weighting. Higher values (0.7+) favor semantic similarity, lower values (0.3-0.5) favor keyword matching (default: 0.7). For pure keyword search, use search_mode='keyword' instead.",
					"minimum":     0.0,
					"maximum":     1.0,
				},
				"tag_boost": map[string]interface{}{
					"type":        "number",
					"description": "When > 0, tags boost rather than filter: results with matching tags rank higher but untagged results are still returned. 0.0 (default) = tags are hard AND filters that exclude non-matching episodes.",
					"minimum":     0.0,
					"maximum":     2.0,
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
		Description: "Update metadata, tags, or expiration of an episode. Setting expired_at to a past timestamp performs a soft-delete — the episode is hidden from default search but remains recoverable by setting expired_at back to null. Use tags to demote (e.g. add 'deprecated') so callers can filter stale content at query time.",
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
					"description": "New tags array (replaces existing tags). Add tags like 'deprecated' to demote episodes that should be filtered out at query time.",
				},
				"expired_at": map[string]interface{}{
					"type":        "string",
					"description": "Expiration time (ISO 8601). Set to a past timestamp to soft-delete (hidden from default search, recoverable). Set to a future timestamp to schedule expiration. Pass null to un-expire.",
				},
				"metadata": map[string]interface{}{
					"type":        "string",
					"description": "JSON string with metadata",
				},
			},
			Required: []string{"id"},
		},
	}, s.handleUpdateEpisode)

	// add_knowledge tool
	s.mcpServer.AddTool(mcp.Tool{
		Name:        "add_knowledge",
		Description: "Store a knowledge fact as a subject-predicate-object triple. Triples link back to their source episode for provenance. Entities are automatically resolved — if a semantically matching entity already exists, it will be reused rather than duplicated.\n\nAllowed predicates: owns, works_at, contributes_to, uses, prefers, builds, depends_on, located_in, related_to, part_of, instance_of, created_by, configured_with, deployed_on, communicates_via",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"subject": map[string]interface{}{
					"type":        "string",
					"description": "The subject entity (e.g., 'Mike', 'Engram project')",
				},
				"predicate": map[string]interface{}{
					"type":        "string",
					"description": "The relationship",
					"enum":        []string{"owns", "works_at", "contributes_to", "uses", "prefers", "builds", "depends_on", "located_in", "related_to", "part_of", "instance_of", "created_by", "configured_with", "deployed_on", "communicates_via"},
				},
				"object": map[string]interface{}{
					"type":        "string",
					"description": "The object entity (e.g., 'DuckDB', 'OscillateLabs')",
				},
				"subject_type": map[string]interface{}{
					"type":        "string",
					"description": "Type of the subject entity",
					"enum":        []string{"person", "project", "tool", "organization", "place", "concept"},
				},
				"object_type": map[string]interface{}{
					"type":        "string",
					"description": "Type of the object entity",
					"enum":        []string{"person", "project", "tool", "organization", "place", "concept"},
				},
				"source_episode_id": map[string]interface{}{
					"type":        "string",
					"description": "Episode ID this fact was derived from",
				},
				"source": map[string]interface{}{
					"type":        "string",
					"description": "Source identifier (e.g., 'claude-code/opus-4.6')",
				},
				"group_id": map[string]interface{}{
					"type":        "string",
					"description": "Group namespace",
				},
			},
			Required: []string{"subject", "predicate", "object", "source"},
		},
	}, s.handleAddKnowledge)

	// link_episodes tool
	s.mcpServer.AddTool(mcp.Tool{
		Name:        "link_episodes",
		Description: "Create a directional link between two episodes that share entities, topics, or narrative continuity. Duplicate links (same source, target, and relationship) are silently skipped.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"source_episode_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the source episode",
				},
				"target_episode_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the target episode",
				},
				"relationship": map[string]interface{}{
					"type":        "string",
					"description": "Type of link between the episodes",
					"enum":        []string{"same_entity", "follows_up", "contradicts", "elaborates", "supersedes"},
				},
				"weight": map[string]interface{}{
					"type":        "number",
					"description": "Link strength 0.0-1.0 (default: 1.0)",
					"minimum":     0.0,
					"maximum":     1.0,
				},
			},
			Required: []string{"source_episode_id", "target_episode_id", "relationship"},
		},
	}, s.handleLinkEpisodes)

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
		MinSimilarity  float64  `json:"min_similarity"`
		SearchMode     string   `json:"search_mode"`
		SearchAlpha    float64  `json:"search_alpha"`
		TagBoost       float64  `json:"tag_boost"`
	}

	if err := parseParams(request.Params.Arguments, &params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
	}

	// Validate search_mode
	if params.SearchMode != "" && params.SearchMode != "vector" && params.SearchMode != "keyword" && params.SearchMode != "hybrid" {
		return mcp.NewToolResultError("search_mode must be 'vector', 'keyword', or 'hybrid'"), nil
	}

	// Validate search_alpha range
	if params.SearchAlpha < 0 || params.SearchAlpha > 1 {
		return mcp.NewToolResultError("search_alpha must be between 0.0 and 1.0"), nil
	}



	// Generate embedding for semantic search (skip for keyword mode)
	var queryEmbedding []float32
	if params.Query != "" && params.SearchMode != "keyword" {
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
		MinSimilarity:  params.MinSimilarity,
		SearchMode:     params.SearchMode,
		SearchAlpha:    params.SearchAlpha,
		TagBoost:       params.TagBoost,
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

func (s *Server) handleAddKnowledge(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		Subject         string `json:"subject"`
		Predicate       string `json:"predicate"`
		Object          string `json:"object"`
		SubjectType     string `json:"subject_type"`
		ObjectType      string `json:"object_type"`
		SourceEpisodeID string `json:"source_episode_id"`
		Source          string `json:"source"`
		GroupID         string `json:"group_id"`
	}

	if err := parseParams(request.Params.Arguments, &params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
	}

	// Validate predicate against controlled vocabulary
	validPredicates := map[string]bool{
		"owns": true, "works_at": true, "contributes_to": true, "uses": true,
		"prefers": true, "builds": true, "depends_on": true, "located_in": true,
		"related_to": true, "part_of": true, "instance_of": true, "created_by": true,
		"configured_with": true, "deployed_on": true, "communicates_via": true,
	}
	if !validPredicates[params.Predicate] {
		return mcp.NewToolResultError(fmt.Sprintf("invalid predicate %q: must be one of: owns, works_at, contributes_to, uses, prefers, builds, depends_on, located_in, related_to, part_of, instance_of, created_by, configured_with, deployed_on, communicates_via", params.Predicate)), nil
	}

	// Resolve subject entity (embed name, match or create)
	embedCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	subjectEmb, err := s.embedder.Generate(embedCtx, params.Subject)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to generate subject embedding: %v\n", err)
	}

	subjectEntity, err := s.store.InsertEntity(ctx, &models.Entity{
		CanonicalName: params.Subject,
		EntityType:    params.SubjectType,
		Embedding:     subjectEmb,
		GroupID:       params.GroupID,
	}, 0.88)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to resolve subject entity: %v", err)), nil
	}

	// Resolve object entity
	embedCtx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	objectEmb, err := s.embedder.Generate(embedCtx2, params.Object)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to generate object embedding: %v\n", err)
	}

	objectEntity, err := s.store.InsertEntity(ctx, &models.Entity{
		CanonicalName: params.Object,
		EntityType:    params.ObjectType,
		Embedding:     objectEmb,
		GroupID:       params.GroupID,
	}, 0.88)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to resolve object entity: %v", err)), nil
	}

	// Generate embedding for the triple itself
	tripleText := fmt.Sprintf("%s %s %s", subjectEntity.CanonicalName, params.Predicate, objectEntity.CanonicalName)
	embedCtx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()

	tripleEmb, err := s.embedder.Generate(embedCtx3, tripleText)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to generate triple embedding: %v\n", err)
	}

	triple := &models.KnowledgeTriple{
		SubjectEntityID: subjectEntity.ID,
		Predicate:       params.Predicate,
		ObjectEntityID:  objectEntity.ID,
		SourceEpisodeID: params.SourceEpisodeID,
		Source:          params.Source,
		GroupID:         params.GroupID,
		Embedding:       tripleEmb,
		Confidence:      1.0, // Client-written triples get full confidence
	}

	if err := s.store.InsertKnowledgeTriple(ctx, triple); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to store knowledge triple: %v", err)), nil
	}

	result, _ := json.Marshal(map[string]interface{}{
		"success":           true,
		"triple_id":         triple.ID,
		"subject_entity_id": subjectEntity.ID,
		"subject_name":      subjectEntity.CanonicalName,
		"object_entity_id":  objectEntity.ID,
		"object_name":       objectEntity.CanonicalName,
		"message":           fmt.Sprintf("Stored: %s %s %s", subjectEntity.CanonicalName, params.Predicate, objectEntity.CanonicalName),
	})

	return mcp.NewToolResultText(string(result)), nil
}

func (s *Server) handleLinkEpisodes(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		SourceEpisodeID string  `json:"source_episode_id"`
		TargetEpisodeID string  `json:"target_episode_id"`
		Relationship    string  `json:"relationship"`
		Weight          float64 `json:"weight"`
	}

	if err := parseParams(request.Params.Arguments, &params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
	}

	// Validate relationship
	validRelationships := map[string]bool{
		"same_entity": true, "follows_up": true, "contradicts": true,
		"elaborates": true, "supersedes": true,
	}
	if !validRelationships[params.Relationship] {
		return mcp.NewToolResultError(fmt.Sprintf("invalid relationship %q: must be one of: same_entity, follows_up, contradicts, elaborates, supersedes", params.Relationship)), nil
	}

	link := &models.EpisodeLink{
		SourceEpisodeID: params.SourceEpisodeID,
		TargetEpisodeID: params.TargetEpisodeID,
		Relationship:    params.Relationship,
		Weight:          params.Weight,
	}

	if err := s.store.InsertEpisodeLink(ctx, link); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to link episodes: %v", err)), nil
	}

	result, _ := json.Marshal(map[string]interface{}{
		"success": true,
		"link_id": link.ID,
		"message": fmt.Sprintf("Linked %s -[%s]-> %s", params.SourceEpisodeID, params.Relationship, params.TargetEpisodeID),
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
