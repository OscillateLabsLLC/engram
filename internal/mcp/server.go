package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/models"
)

// Embedder generates vector embeddings for text
type Embedder interface {
	Generate(ctx context.Context, text string) ([]float32, error)
	// Model returns the embedding model name, used to stamp provenance
	Model() string
}

// Server implements the MCP server for Engram
type Server struct {
	store     *db.Store
	embedder  Embedder
	mcpServer *server.MCPServer
}

// NewServer creates a new MCP server
func NewServer(store *db.Store, embedder Embedder) *Server {
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
				"graph_depth": map[string]interface{}{
					"type":        "integer",
					"description": "When > 0, walk the episode link graph this many hops from each of the top results and attach the linked episode IDs and relationships under 'linked_episodes'. 0 (default) = no graph traversal.",
					"minimum":     0,
					"maximum":     3,
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
		Description: "Store a knowledge fact as a subject-predicate-object triple. Triples link back to their source episode for provenance. Entities are automatically resolved — if a semantically matching entity already exists, it will be reused rather than duplicated. Prefer the most specific predicate available; use related_to only when no other predicate fits.\n\nAllowed predicates: " + strings.Join(models.SortedPredicates(), ", "),
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
					"enum":        models.SortedPredicates(),
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

	// search_knowledge tool
	s.mcpServer.AddTool(mcp.Tool{
		Name:        "search_knowledge",
		Description: "Search knowledge triples (subject-predicate-object facts) by semantic similarity. Returns matching facts with their subject and object entity names, confidence, and provenance.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Natural language text to search knowledge facts for",
				},
				"group_id": map[string]interface{}{
					"type":        "string",
					"description": "Advanced filter: narrow results to a specific group namespace. Omit this in almost all cases.",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of triples to return (default: 10)",
				},
				"min_similarity": map[string]interface{}{
					"type":        "number",
					"description": "Minimum cosine similarity threshold (default: 0.35)",
					"minimum":     0.0,
					"maximum":     1.0,
				},
				"include_ungrounded": map[string]interface{}{
					"type":        "boolean",
					"description": "Include quarantined triples that could not be grounded in their source episode text (default: false)",
				},
			},
			Required: []string{"query"},
		},
	}, s.handleSearchKnowledge)

	// add_conversation tool
	s.mcpServer.AddTool(mcp.Tool{
		Name:        "add_conversation",
		Description: "Store a multi-turn conversation as a single episode. Messages are formatted into readable content and the raw message array is preserved in metadata. Knowledge extraction happens asynchronously via the dreamer — this tool does not create triples.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"messages": map[string]interface{}{
					"type":        "array",
					"description": "Conversation turns in order",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"role": map[string]interface{}{
								"type":        "string",
								"description": "Speaker role (e.g., 'user', 'assistant')",
							},
							"content": map[string]interface{}{
								"type":        "string",
								"description": "Message text",
							},
						},
						"required": []string{"role", "content"},
					},
				},
				"source": map[string]interface{}{
					"type":        "string",
					"description": "Identifier for what created this memory (e.g., 'claude-desktop', 'claude-code')",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Human-readable label for the conversation",
				},
				"group_id": map[string]interface{}{
					"type":        "string",
					"description": "Advanced: namespace for multi-tenant isolation. Omit this in almost all cases.",
				},
				"tags": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "string",
					},
					"description": "Tags for categorization",
				},
				"metadata": map[string]interface{}{
					"type":        "string",
					"description": "JSON string with additional metadata (merged with the stored message array)",
				},
			},
			Required: []string{"messages", "source"},
		},
	}, s.handleAddConversation)

	// find_loose_ends tool
	s.mcpServer.AddTool(mcp.Tool{
		Name:        "find_loose_ends",
		Description: "Surface weakly-connected corners of the memory graph: episodes with no links and no derived knowledge, entities that appear in only one fact, small isolated clusters of linked episodes, and recurring dreams — quarantined facts the Dreamer keeps re-extracting from different episodes that nothing has confirmed. Recurring dreams are worth raising with the user for confirmation or rejection via resolve_knowledge.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"group_id": map[string]interface{}{
					"type":        "string",
					"description": "Advanced filter: narrow results to a specific group namespace. Omit this in almost all cases.",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum items per section (default: 10)",
				},
			},
			Required: []string{},
		},
	}, s.handleFindLooseEnds)

	// resolve_knowledge tool
	s.mcpServer.AddTool(mcp.Tool{
		Name:        "resolve_knowledge",
		Description: "Record the user's verdict on a knowledge triple, typically a recurring dream surfaced by find_loose_ends. Only use after the user has explicitly confirmed or rejected the fact in conversation. Confirm promotes it to grounded and verified; reject expires it (demoted, never deleted).",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"triple_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the knowledge triple to resolve",
				},
				"action": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"confirm", "reject"},
					"description": "confirm: the user says this fact is true. reject: the user says it is not.",
				},
			},
			Required: []string{"triple_id", "action"},
		},
	}, s.handleResolveKnowledge)

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
		EmbeddingModel:    s.embedder.Model(),
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
		GraphDepth     int      `json:"graph_depth"`
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

	// Validate graph_depth range
	if params.GraphDepth < 0 || params.GraphDepth > 3 {
		return mcp.NewToolResultError("graph_depth must be between 0 and 3"), nil
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

	// Graph-aware search: attach linked episodes to the top results. Purely
	// additive — the payload is unchanged when graph_depth is 0/absent.
	if params.GraphDepth > 0 && len(episodes) > 0 {
		result, _ := json.Marshal(s.attachLinkedEpisodes(ctx, episodes, params.GraphDepth))
		return mcp.NewToolResultText(string(result)), nil
	}

	result, _ := json.Marshal(episodes)
	return mcp.NewToolResultText(string(result)), nil
}

// linkedEpisode is a compact reference to an episode reached via graph traversal
type linkedEpisode struct {
	EpisodeID    string `json:"episode_id"`
	Relationship string `json:"relationship"`
	ViaEntityID  string `json:"via_entity_id,omitempty"`
}

// episodeWithLinks decorates a search result with graph traversal output
type episodeWithLinks struct {
	models.Episode
	LinkedEpisodes []linkedEpisode `json:"linked_episodes,omitempty"`
}

// graphSearchTopResults is how many top search results get graph traversal
const graphSearchTopResults = 5

// attachLinkedEpisodes walks the episode link graph from each of the top
// results and attaches the episodes it reaches. Traversal failures are
// logged and skipped — graph decoration never fails a search.
func (s *Server) attachLinkedEpisodes(ctx context.Context, episodes []models.Episode, depth int) []episodeWithLinks {
	enriched := make([]episodeWithLinks, len(episodes))
	for i, ep := range episodes {
		enriched[i].Episode = ep
		if i >= graphSearchTopResults {
			continue
		}
		links, err := s.store.TraverseEpisodeLinks(ctx, ep.ID, depth)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: graph traversal failed for episode %s: %v\n", ep.ID, err)
			continue
		}
		seen := map[string]bool{ep.ID: true}
		for _, link := range links {
			for _, id := range []string{link.SourceEpisodeID, link.TargetEpisodeID} {
				if !seen[id] {
					seen[id] = true
					enriched[i].LinkedEpisodes = append(enriched[i].LinkedEpisodes, linkedEpisode{
						EpisodeID:    id,
						Relationship: link.Relationship,
						ViaEntityID:  link.ViaEntityID,
					})
				}
			}
		}
	}
	return enriched
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
	if !models.ValidPredicates[params.Predicate] {
		return mcp.NewToolResultError(fmt.Sprintf("invalid predicate %q: must be one of: %s", params.Predicate, strings.Join(models.SortedPredicates(), ", "))), nil
	}

	// Resolve subject entity (embed name, match or create)
	embedCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	subjectEmb, err := s.embedder.Generate(embedCtx, params.Subject)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to generate subject embedding: %v\n", err)
	}

	subjectEntity, err := s.store.InsertEntity(ctx, &models.Entity{
		CanonicalName:  params.Subject,
		EntityType:     params.SubjectType,
		Embedding:      subjectEmb,
		EmbeddingModel: s.embedder.Model(),
		GroupID:        params.GroupID,
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
		CanonicalName:  params.Object,
		EntityType:     params.ObjectType,
		Embedding:      objectEmb,
		EmbeddingModel: s.embedder.Model(),
		GroupID:        params.GroupID,
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
		EmbeddingModel:  s.embedder.Model(),
		Confidence:      1.0,  // Client-written triples get full confidence
		Grounded:        true, // Manually added triples are grounded by definition
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

// groupIDPattern restricts group_id values passed into SearchKnowledge, which
// builds its group filter by interpolation
var groupIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func (s *Server) handleSearchKnowledge(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		Query             string  `json:"query"`
		GroupID           string  `json:"group_id"`
		MaxResults        int     `json:"max_results"`
		MinSimilarity     float64 `json:"min_similarity"`
		IncludeUngrounded bool    `json:"include_ungrounded"`
	}

	if err := parseParams(request.Params.Arguments, &params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
	}
	if params.Query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	if params.GroupID != "" && !groupIDPattern.MatchString(params.GroupID) {
		return mcp.NewToolResultError("group_id may only contain letters, digits, '.', '_' and '-'"), nil
	}

	embedCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	emb, err := s.embedder.Generate(embedCtx, params.Query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to embed query (knowledge search is vector-based): %v", err)), nil
	}

	// Store applies defaults: max_results 10, min_similarity 0.35
	triples, err := s.store.SearchKnowledge(ctx, emb, params.GroupID, params.MaxResults, params.MinSimilarity, params.IncludeUngrounded)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("knowledge search failed: %v", err)), nil
	}
	if triples == nil {
		triples = []models.KnowledgeTriple{}
	}

	result, _ := json.Marshal(triples)
	return mcp.NewToolResultText(string(result)), nil
}

func (s *Server) handleAddConversation(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Source   string   `json:"source"`
		Name     string   `json:"name"`
		GroupID  string   `json:"group_id"`
		Tags     []string `json:"tags"`
		Metadata string   `json:"metadata"`
	}

	if err := parseParams(request.Params.Arguments, &params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
	}
	if len(params.Messages) == 0 {
		return mcp.NewToolResultError("messages is required and must not be empty"), nil
	}
	if params.Source == "" {
		return mcp.NewToolResultError("source is required"), nil
	}

	// Format turns into readable content
	var sb strings.Builder
	for _, msg := range params.Messages {
		sb.WriteString(msg.Role)
		sb.WriteString(": ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
	}
	content := sb.String()

	// Preserve the raw message array in metadata, merged with caller metadata
	meta := map[string]interface{}{}
	if params.Metadata != "" {
		if err := json.Unmarshal([]byte(params.Metadata), &meta); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("metadata must be a JSON object: %v", err)), nil
		}
	}
	meta["messages"] = params.Messages
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode metadata: %v", err)), nil
	}

	embedCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	emb, err := s.embedder.Generate(embedCtx, content)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to generate embedding: %v\n", err)
		emb = nil
	}

	ep := &models.Episode{
		Content:        content,
		Name:           params.Name,
		Source:         params.Source,
		GroupID:        params.GroupID,
		Tags:           params.Tags,
		Embedding:      emb,
		EmbeddingModel: s.embedder.Model(),
		Metadata:       string(metaJSON),
	}

	if err := s.store.InsertEpisode(ctx, ep); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to store conversation: %v", err)), nil
	}

	result, _ := json.Marshal(map[string]interface{}{
		"success":  true,
		"id":       ep.ID,
		"messages": len(params.Messages),
		"message":  "Conversation stored successfully",
	})

	return mcp.NewToolResultText(string(result)), nil
}

func (s *Server) handleFindLooseEnds(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		GroupID string `json:"group_id"`
		Limit   int    `json:"limit"`
	}

	if err := parseParams(request.Params.Arguments, &params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
	}
	if params.Limit <= 0 {
		params.Limit = 10
	}

	looseEnds, err := s.store.FindLooseEnds(ctx, params.GroupID, params.Limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to find loose ends: %v", err)), nil
	}

	result, _ := json.Marshal(looseEnds)
	return mcp.NewToolResultText(string(result)), nil
}

func (s *Server) handleResolveKnowledge(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params struct {
		TripleID string `json:"triple_id"`
		Action   string `json:"action"`
	}

	if err := parseParams(request.Params.Arguments, &params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid parameters: %v", err)), nil
	}
	if params.TripleID == "" {
		return mcp.NewToolResultError("triple_id is required"), nil
	}
	if params.Action != "confirm" && params.Action != "reject" {
		return mcp.NewToolResultError("action must be 'confirm' or 'reject'"), nil
	}

	if err := s.store.ResolveKnowledge(ctx, params.TripleID, params.Action == "confirm"); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to resolve knowledge: %v", err)), nil
	}

	result, _ := json.Marshal(map[string]interface{}{
		"success": true,
		"id":      params.TripleID,
		"action":  params.Action,
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
