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
	Similarity        *float64   `json:"similarity,omitempty"`
	Relevance         *float64   `json:"relevance,omitempty"`
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
	MinSimilarity  float64    `json:"min_similarity,omitempty"` // Minimum cosine similarity threshold (0.0-1.0)
	SearchMode     string     `json:"search_mode,omitempty"`    // "vector" (default), "keyword", or "hybrid"
	SearchAlpha    float64    `json:"search_alpha,omitempty"`   // Hybrid weighting: 0.0 = BM25 only, 1.0 = cosine only (default: 0.7)
	TagBoost       float64    `json:"tag_boost,omitempty"`      // 0.0 = hard filter (default), >0 = boost tag matches by this weight
}

// UpdateParams defines parameters for updating an episode
type UpdateParams struct {
	Tags      *[]string  `json:"tags,omitempty"`
	ExpiredAt *time.Time `json:"expired_at,omitempty"`
	Metadata  *string    `json:"metadata,omitempty"`
}

// Entity represents a canonical entity in the knowledge graph
type Entity struct {
	ID            string    `json:"id"`
	CanonicalName string    `json:"canonical_name"`
	EntityType    string    `json:"entity_type,omitempty"` // person, project, tool, org, concept
	Embedding     []float32 `json:"embedding,omitempty"`
	GroupID       string    `json:"group_id"`
	CreatedAt     time.Time `json:"created_at"`
	Metadata      string    `json:"metadata,omitempty"` // JSON string
}

// KnowledgeTriple represents a subject-predicate-object fact in the knowledge graph
type KnowledgeTriple struct {
	ID              string     `json:"id"`
	SubjectEntityID string     `json:"subject_entity_id"`
	Predicate       string     `json:"predicate"`
	ObjectEntityID  string     `json:"object_entity_id"`
	SourceEpisodeID string     `json:"source_episode_id,omitempty"`
	Source          string     `json:"source"`
	GroupID         string     `json:"group_id"`
	Embedding       []float32  `json:"embedding,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	ExpiredAt       *time.Time `json:"expired_at,omitempty"`
	Confidence      float64    `json:"confidence"`
	Verified        bool       `json:"verified"`
	Metadata        string     `json:"metadata,omitempty"` // JSON string

	// Denormalized fields populated during reads
	SubjectName string `json:"subject_name,omitempty"`
	ObjectName  string `json:"object_name,omitempty"`
}

// EpisodeLink represents a directional link between two episodes
type EpisodeLink struct {
	ID              string    `json:"id"`
	SourceEpisodeID string    `json:"source_episode_id"`
	TargetEpisodeID string    `json:"target_episode_id"`
	Relationship    string    `json:"relationship"` // same_entity, follows_up, contradicts, elaborates, supersedes
	ViaEntityID     string    `json:"via_entity_id,omitempty"`
	Weight          float64   `json:"weight"`
	CreatedAt       time.Time `json:"created_at"`
}
