package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oscillatelabsllc/engram/internal/models"
)

// AddMemoryRequest represents the request body for adding a memory
type AddMemoryRequest struct {
	Content           string   `json:"content"`
	Name              string   `json:"name,omitempty"`
	Source            string   `json:"source"`
	SourceModel       string   `json:"source_model,omitempty"`
	SourceDescription string   `json:"source_description,omitempty"`
	GroupID           string   `json:"group_id,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	ValidAt           string   `json:"valid_at,omitempty"`
	Metadata          string   `json:"metadata,omitempty"`
}

// SearchRequest represents the request parameters for searching memories
type SearchRequest struct {
	Query          string   `json:"query,omitempty"`
	GroupID        string   `json:"group_id,omitempty"`
	MaxResults     int      `json:"max_results,omitempty"`
	Before         string   `json:"before,omitempty"`
	After          string   `json:"after,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Source         string   `json:"source,omitempty"`
	IncludeExpired bool     `json:"include_expired,omitempty"`
}

// GetEpisodesRequest represents query parameters for getting episodes
type GetEpisodesRequest struct {
	GroupID    string `json:"group_id,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
	Before     string `json:"before,omitempty"`
	After      string `json:"after,omitempty"`
}

// UpdateEpisodeRequest represents the request body for updating an episode
type UpdateEpisodeRequest struct {
	Tags      *[]string `json:"tags,omitempty"`
	ExpiresAt *string   `json:"expires_at,omitempty"`
	Metadata  *string   `json:"metadata,omitempty"`
}

// handleAddMemory processes requests to add a new memory
func (s *Server) handleAddMemory(w http.ResponseWriter, r *http.Request) {
	var req AddMemoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}

	// Validate required fields
	if req.Content == "" {
		errorResponse(w, http.StatusBadRequest, "content is required")
		return
	}
	if req.Source == "" {
		errorResponse(w, http.StatusBadRequest, "source is required")
		return
	}

	// Set defaults
	if req.GroupID == "" {
		req.GroupID = "default"
	}

	// Parse valid_at time
	var validAt *time.Time
	if req.ValidAt != "" {
		t, err := time.Parse(time.RFC3339, req.ValidAt)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid valid_at format, use ISO 8601")
			return
		}
		validAt = &t
	}

	// Generate embedding
	embedCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var embedding []float32
	emb, err := s.embedder.Generate(embedCtx, req.Content)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to generate embedding: %v\n", err)
	} else {
		embedding = emb
		fmt.Fprintf(os.Stderr, "Success: Generated embedding with %d dimensions\n", len(emb))
	}

	// Create episode
	episode := &models.Episode{
		Name:              req.Name,
		Content:           req.Content,
		Source:            req.Source,
		SourceModel:       req.SourceModel,
		SourceDescription: req.SourceDescription,
		GroupID:           req.GroupID,
		Tags:              req.Tags,
		ValidAt:           validAt,
		Metadata:          req.Metadata,
		Embedding:         embedding,
	}

	// Store in database
	if err := s.store.InsertEpisode(r.Context(), episode); err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to store episode: "+err.Error())
		return
	}

	// Return created episode
	successResponse(w, map[string]interface{}{
		"success":  true,
		"episode":  episode,
		"embedded": len(embedding) > 0,
	})
}

// handleSearch processes search requests
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	var req SearchRequest

	// Try JSON body first, fall back to query params
	if r.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			errorResponse(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
			return
		}
	} else {
		// Parse from query parameters
		req.Query = r.URL.Query().Get("query")
		req.GroupID = r.URL.Query().Get("group_id")
		req.Source = r.URL.Query().Get("source")
		req.Before = r.URL.Query().Get("before")
		req.After = r.URL.Query().Get("after")

		if maxResults := r.URL.Query().Get("max_results"); maxResults != "" {
			fmt.Sscanf(maxResults, "%d", &req.MaxResults)
		}
	}

	// Set defaults
	if req.GroupID == "" {
		req.GroupID = "default"
	}
	if req.MaxResults == 0 {
		req.MaxResults = 10
	}

	// Generate embedding for query if provided
	var queryEmbedding []float32
	if req.Query != "" {
		embedCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		emb, err := s.embedder.Generate(embedCtx, req.Query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to generate query embedding: %v\n", err)
		} else {
			queryEmbedding = emb
			fmt.Fprintf(os.Stderr, "Success: Generated query embedding with %d dimensions\n", len(emb))
		}
	}

	// Parse time filters
	var beforeTime, afterTime *time.Time
	if req.Before != "" {
		t, err := time.Parse(time.RFC3339, req.Before)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid before format, use ISO 8601")
			return
		}
		beforeTime = &t
	}
	if req.After != "" {
		t, err := time.Parse(time.RFC3339, req.After)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid after format, use ISO 8601")
			return
		}
		afterTime = &t
	}

	// Search episodes using the existing Search method
	episodes, err := s.store.Search(r.Context(), models.SearchParams{
		Query:          req.Query,
		QueryEmbedding: queryEmbedding,
		GroupID:        req.GroupID,
		MaxResults:     req.MaxResults,
		Before:         beforeTime,
		After:          afterTime,
		Tags:           req.Tags,
		Source:         req.Source,
		IncludeExpired: req.IncludeExpired,
	})

	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Search failed: "+err.Error())
		return
	}

	successResponse(w, map[string]interface{}{
		"episodes": episodes,
		"count":    len(episodes),
	})
}

// handleGetEpisodes retrieves episodes by time range
func (s *Server) handleGetEpisodes(w http.ResponseWriter, r *http.Request) {
	var req GetEpisodesRequest

	// Parse query parameters
	req.GroupID = r.URL.Query().Get("group_id")
	req.Before = r.URL.Query().Get("before")
	req.After = r.URL.Query().Get("after")

	if maxResults := r.URL.Query().Get("max_results"); maxResults != "" {
		fmt.Sscanf(maxResults, "%d", &req.MaxResults)
	}

	// Set defaults
	if req.GroupID == "" {
		req.GroupID = "default"
	}
	if req.MaxResults == 0 {
		req.MaxResults = 10
	}

	// Parse time filters
	var beforeTime, afterTime *time.Time
	if req.Before != "" {
		t, err := time.Parse(time.RFC3339, req.Before)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid before format, use ISO 8601")
			return
		}
		beforeTime = &t
	}
	if req.After != "" {
		t, err := time.Parse(time.RFC3339, req.After)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid after format, use ISO 8601")
			return
		}
		afterTime = &t
	}

	// Use Search method without query for chronological listing
	episodes, err := s.store.Search(r.Context(), models.SearchParams{
		GroupID:    req.GroupID,
		MaxResults: req.MaxResults,
		Before:     beforeTime,
		After:      afterTime,
	})

	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to get episodes: "+err.Error())
		return
	}

	successResponse(w, map[string]interface{}{
		"episodes": episodes,
		"count":    len(episodes),
	})
}

// handleUpdateEpisode updates an episode's metadata
func (s *Server) handleUpdateEpisode(w http.ResponseWriter, r *http.Request) {
	episodeID := chi.URLParam(r, "id")
	if episodeID == "" {
		errorResponse(w, http.StatusBadRequest, "episode id is required")
		return
	}

	var req UpdateEpisodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}

	// Parse expires_at if provided
	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid expires_at format, use ISO 8601")
			return
		}
		expiresAt = &t
	}

	// Update episode
	err := s.store.UpdateEpisode(r.Context(), episodeID, models.UpdateParams{
		Tags:      req.Tags,
		ExpiredAt: expiresAt,
		Metadata:  req.Metadata,
	})

	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to update episode: "+err.Error())
		return
	}

	successResponse(w, map[string]interface{}{
		"success": true,
		"message": "Episode updated successfully",
	})
}

// handleGetStatus returns system status
func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Use Search method to get episode count
	episodes, err := s.store.Search(ctx, models.SearchParams{
		GroupID:    "default",
		MaxResults: 1000000, // Large number to get all
	})

	count := 0
	if err == nil {
		count = len(episodes)
	}

	successResponse(w, map[string]interface{}{
		"status":         "operational",
		"episode_count":  count,
		"database_ready": err == nil,
	})
}
