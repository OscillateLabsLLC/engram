package api

import (
	"encoding/json"
	"net/http"
)

// handleOpenAPISpec returns the OpenAPI 3.0 specification
func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	spec := map[string]interface{}{
		"openapi": "3.0.0",
		"info": map[string]interface{}{
			"title":       "Engram Memory System API",
			"description": "API for storing and retrieving episodic memories with semantic search capabilities",
			"version":     "1.0.0",
			"contact": map[string]interface{}{
				"name": "Oscillate Labs",
				"url":  "https://github.com/oscillatelabsllc/engram",
			},
			"license": map[string]interface{}{
				"name": "MIT",
				"url":  "https://opensource.org/licenses/MIT",
			},
		},
		"servers": []map[string]interface{}{
			{
				"url":         "http://localhost:8080",
				"description": "Local development server",
			},
		},
		"paths": map[string]interface{}{
			"/health": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Health check",
					"description": "Check if the server is running",
					"operationId": "getHealth",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Server is healthy",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"status": map[string]interface{}{
												"type": "string",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			"/api/v1/memory": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Add a new memory",
					"description": "Store a new episode in memory with optional embedding",
					"operationId": "addMemory",
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"$ref": "#/components/schemas/AddMemoryRequest",
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Memory added successfully",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/AddMemoryResponse",
									},
								},
							},
						},
						"400": map[string]interface{}{
							"description": "Invalid request",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/ErrorResponse",
									},
								},
							},
						},
					},
				},
			},
			"/api/v1/memory/search": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Search memories",
					"description": "Search episodes using semantic similarity, temporal, and tag filters",
					"operationId": "searchMemories",
					"parameters": []map[string]interface{}{
						{
							"name":        "query",
							"in":          "query",
							"description": "Text to search for (will be embedded)",
							"schema": map[string]interface{}{
								"type": "string",
							},
						},
						{
							"name":        "group_id",
							"in":          "query",
							"description": "Filter by group ID",
							"schema": map[string]interface{}{
								"type":    "string",
								"default": "default",
							},
						},
						{
							"name":        "max_results",
							"in":          "query",
							"description": "Maximum number of results",
							"schema": map[string]interface{}{
								"type":    "integer",
								"default": 10,
							},
						},
						{
							"name":        "before",
							"in":          "query",
							"description": "Episodes created before this time (ISO 8601)",
							"schema": map[string]interface{}{
								"type":   "string",
								"format": "date-time",
							},
						},
						{
							"name":        "after",
							"in":          "query",
							"description": "Episodes created after this time (ISO 8601)",
							"schema": map[string]interface{}{
								"type":   "string",
								"format": "date-time",
							},
						},
						{
							"name":        "source",
							"in":          "query",
							"description": "Filter by source client",
							"schema": map[string]interface{}{
								"type": "string",
							},
						},
						{
							"name":        "include_expired",
							"in":          "query",
							"description": "Include expired episodes",
							"schema": map[string]interface{}{
								"type":    "boolean",
								"default": false,
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Search results",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/SearchResponse",
									},
								},
							},
						},
					},
				},
			},
			"/api/v1/memory/episodes": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Get episodes",
					"description": "Retrieve episodes by time range, source, or group",
					"operationId": "getEpisodes",
					"parameters": []map[string]interface{}{
						{
							"name":        "group_id",
							"in":          "query",
							"description": "Filter by group ID",
							"schema": map[string]interface{}{
								"type":    "string",
								"default": "default",
							},
						},
						{
							"name":        "max_results",
							"in":          "query",
							"description": "Maximum number of results",
							"schema": map[string]interface{}{
								"type":    "integer",
								"default": 10,
							},
						},
						{
							"name":        "before",
							"in":          "query",
							"description": "Episodes created before this time (ISO 8601)",
							"schema": map[string]interface{}{
								"type":   "string",
								"format": "date-time",
							},
						},
						{
							"name":        "after",
							"in":          "query",
							"description": "Episodes created after this time (ISO 8601)",
							"schema": map[string]interface{}{
								"type":   "string",
								"format": "date-time",
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Episodes retrieved successfully",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/EpisodesResponse",
									},
								},
							},
						},
					},
				},
			},
			"/api/v1/memory/episodes/{id}": map[string]interface{}{
				"put": map[string]interface{}{
					"summary":     "Update episode",
					"description": "Update metadata, tags, or expiration of an episode",
					"operationId": "updateEpisode",
					"parameters": []map[string]interface{}{
						{
							"name":        "id",
							"in":          "path",
							"required":    true,
							"description": "Episode ID",
							"schema": map[string]interface{}{
								"type": "string",
							},
						},
					},
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"$ref": "#/components/schemas/UpdateEpisodeRequest",
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Episode updated successfully",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"success": map[string]interface{}{
												"type": "boolean",
											},
											"message": map[string]interface{}{
												"type": "string",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			"/api/v1/status": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Get system status",
					"description": "Returns current system status and episode count",
					"operationId": "getStatus",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "System status",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/StatusResponse",
									},
								},
							},
						},
					},
				},
			},
		},
		"components": map[string]interface{}{
			"schemas": map[string]interface{}{
				"AddMemoryRequest": map[string]interface{}{
					"type": "object",
					"required": []string{"content", "source"},
					"properties": map[string]interface{}{
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
							"description": "Source client (e.g., 'open-webui', 'claude-desktop')",
						},
						"source_model": map[string]interface{}{
							"type":        "string",
							"description": "Model that created this episode",
						},
						"source_description": map[string]interface{}{
							"type":        "string",
							"description": "Freeform context about the episode",
						},
						"group_id": map[string]interface{}{
							"type":        "string",
							"description": "Group ID for multi-tenant support",
							"default":     "default",
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
							"format":      "date-time",
							"description": "When the information became true (ISO 8601)",
						},
						"metadata": map[string]interface{}{
							"type":        "string",
							"description": "JSON string with additional metadata",
						},
					},
				},
				"AddMemoryResponse": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"success": map[string]interface{}{
							"type": "boolean",
						},
						"episode": map[string]interface{}{
							"$ref": "#/components/schemas/Episode",
						},
						"embedded": map[string]interface{}{
							"type":        "boolean",
							"description": "Whether embedding was generated",
						},
					},
				},
				"UpdateEpisodeRequest": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"tags": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "string",
							},
						},
						"expires_at": map[string]interface{}{
							"type":   "string",
							"format": "date-time",
						},
						"metadata": map[string]interface{}{
							"type": "string",
						},
					},
				},
				"SearchResponse": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"episodes": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"$ref": "#/components/schemas/Episode",
							},
						},
						"count": map[string]interface{}{
							"type": "integer",
						},
					},
				},
				"EpisodesResponse": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"episodes": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"$ref": "#/components/schemas/Episode",
							},
						},
						"count": map[string]interface{}{
							"type": "integer",
						},
					},
				},
				"StatusResponse": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"status": map[string]interface{}{
							"type": "string",
						},
						"episode_count": map[string]interface{}{
							"type": "integer",
						},
						"database_ready": map[string]interface{}{
							"type": "boolean",
						},
					},
				},
				"Episode": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{
							"type": "string",
						},
						"name": map[string]interface{}{
							"type": "string",
						},
						"content": map[string]interface{}{
							"type": "string",
						},
						"source": map[string]interface{}{
							"type": "string",
						},
						"source_model": map[string]interface{}{
							"type": "string",
						},
						"source_description": map[string]interface{}{
							"type": "string",
						},
						"group_id": map[string]interface{}{
							"type": "string",
						},
						"tags": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "string",
							},
						},
						"created_at": map[string]interface{}{
							"type":   "string",
							"format": "date-time",
						},
						"valid_at": map[string]interface{}{
							"type":   "string",
							"format": "date-time",
						},
						"expires_at": map[string]interface{}{
							"type":   "string",
							"format": "date-time",
						},
						"metadata": map[string]interface{}{
							"type": "string",
						},
					},
				},
				"ErrorResponse": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"error": map[string]interface{}{
							"type": "string",
						},
					},
				},
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(spec)
}
