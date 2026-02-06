package models

import "time"

// Episode represents a memory episode in the system
type Episode struct {
	ID                string     `json:"id"`
	Content           string     `json:"content"`
	Name              string     `json:"name,omitempty"`
	Source            string     `json:"source"`
	SourceModel       string     `json:"source_model,omitempty"`
	SourceDescription string     `json:"source_description,omitempty"`
	GroupID           string     `json:"group_id"`
	Tags              []string   `json:"tags,omitempty"`
	Embedding         []float32  `json:"embedding,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	ValidAt           *time.Time `json:"valid_at,omitempty"`
	ExpiredAt         *time.Time `json:"expired_at,omitempty"`
	Metadata          string     `json:"metadata,omitempty"` // JSON string
}

// SearchParams defines parameters for searching episodes
type SearchParams struct {
	Query          string     `json:"query"`
	QueryEmbedding []float32  `json:"query_embedding,omitempty"` // Embedding vector for semantic search
	GroupID        string     `json:"group_id"`
	MaxResults     int        `json:"max_results"`
	Before         *time.Time `json:"before,omitempty"`
	After          *time.Time `json:"after,omitempty"`
	Tags           []string   `json:"tags,omitempty"`
	Source         string     `json:"source,omitempty"`
	IncludeExpired bool       `json:"include_expired"`
}

// UpdateParams defines parameters for updating an episode
type UpdateParams struct {
	Tags      *[]string  `json:"tags,omitempty"`
	ExpiredAt *time.Time `json:"expired_at,omitempty"`
	Metadata  *string    `json:"metadata,omitempty"`
}
