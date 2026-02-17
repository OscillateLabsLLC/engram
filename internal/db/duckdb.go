package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/google/uuid"
	"github.com/oscillatelabsllc/engram/internal/models"
)

// Store wraps DuckDB operations
type Store struct {
	db *sql.DB
}

// NewStore creates a new DuckDB store
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &Store{db: db}
	if err := store.initialize(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return store, nil
}

// initialize sets up the database schema and extensions
func (s *Store) initialize() error {
	schema := `
		-- Install and load VSS extension
		INSTALL vss;
		LOAD vss;

		-- Create episodes table if it doesn't exist
		CREATE TABLE IF NOT EXISTS episodes (
			id VARCHAR PRIMARY KEY,
			content TEXT NOT NULL,
			name VARCHAR,
			source VARCHAR NOT NULL,
			source_model VARCHAR,
			source_description TEXT,
			group_id VARCHAR DEFAULT 'default',
			tags VARCHAR[],
			embedding FLOAT[768],
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			valid_at TIMESTAMPTZ,
			expired_at TIMESTAMPTZ,
			metadata JSON
		);

		-- Create indices if they don't exist
		CREATE INDEX IF NOT EXISTS idx_episodes_created_at ON episodes (created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_episodes_group_id ON episodes (group_id);
		CREATE INDEX IF NOT EXISTS idx_episodes_valid_at ON episodes (valid_at);
		-- Note: No index on expired_at due to DuckDB limitation with UPDATE on indexed TIMESTAMP columns
		CREATE INDEX IF NOT EXISTS idx_episodes_source ON episodes (source);
	`

	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to execute schema: %w", err)
	}

	// Run migrations for existing databases
	if err := s.migrate(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Try to create HNSW index (will fail if already exists, which is fine)
	// Note: VSS extension syntax may vary, this is a placeholder
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_episodes_embedding ON episodes USING HNSW (embedding)")

	return nil
}

// migrate handles schema migrations for existing databases
func (s *Store) migrate() error {
	// Migration 1: TIMESTAMP -> TIMESTAMPTZ for timezone-aware comparisons
	// Check if columns need migration by querying the schema
	var colType string
	err := s.db.QueryRow(`
		SELECT data_type 
		FROM information_schema.columns 
		WHERE table_name = 'episodes' AND column_name = 'created_at'
	`).Scan(&colType)

	if err != nil {
		// Table might not exist yet or other error - skip migration
		return nil
	}

	// If it's still TIMESTAMP (not TIMESTAMP WITH TIME ZONE), migrate
	// Use table recreation approach to avoid DuckDB dependency issues
	if colType == "TIMESTAMP" {
		fmt.Fprintf(os.Stderr, "Migrating timestamp columns to TIMESTAMPTZ...\n")

		migrations := []string{
			// Create new table with correct schema
			`CREATE TABLE episodes_new (
				id VARCHAR PRIMARY KEY,
				content TEXT NOT NULL,
				name VARCHAR,
				source VARCHAR NOT NULL,
				source_model VARCHAR,
				source_description TEXT,
				group_id VARCHAR DEFAULT 'default',
				tags VARCHAR[],
				embedding FLOAT[768],
				created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
				valid_at TIMESTAMPTZ,
				expired_at TIMESTAMPTZ,
				metadata JSON
			)`,
			// Copy data, casting timestamps
			`INSERT INTO episodes_new 
				SELECT id, content, name, source, source_model, source_description,
				       group_id, tags, embedding,
				       created_at::TIMESTAMPTZ, valid_at::TIMESTAMPTZ, expired_at::TIMESTAMPTZ,
				       metadata
				FROM episodes`,
			// Drop old table (this also drops its indexes)
			`DROP TABLE episodes`,
			// Rename new table
			`ALTER TABLE episodes_new RENAME TO episodes`,
			// Recreate indexes
			`CREATE INDEX idx_episodes_created_at ON episodes (created_at DESC)`,
			`CREATE INDEX idx_episodes_group_id ON episodes (group_id)`,
			`CREATE INDEX idx_episodes_valid_at ON episodes (valid_at)`,
			`CREATE INDEX idx_episodes_source ON episodes (source)`,
		}

		for _, migration := range migrations {
			if _, err := s.db.Exec(migration); err != nil {
				return fmt.Errorf("migration failed (%s): %w", migration, err)
			}
		}

		fmt.Fprintf(os.Stderr, "Migration complete.\n")
	}

	return nil
}

// InsertEpisode adds a new episode to the store
func (s *Store) InsertEpisode(ctx context.Context, ep *models.Episode) error {
	if ep.ID == "" {
		ep.ID = uuid.New().String()
	}
	if ep.CreatedAt.IsZero() {
		ep.CreatedAt = time.Now()
	}
	if ep.GroupID == "" {
		ep.GroupID = "default"
	}

	// Convert tags to JSON for DuckDB LIST type
	var tagsJSON interface{}
	if len(ep.Tags) > 0 {
		tagsData, _ := json.Marshal(ep.Tags)
		tagsJSON = string(tagsData)
	} else {
		tagsJSON = nil
	}

	// Convert embedding to JSON for DuckDB FLOAT[] type
	var embeddingJSON interface{}
	if len(ep.Embedding) > 0 {
		embeddingData, _ := json.Marshal(ep.Embedding)
		embeddingJSON = string(embeddingData)
	} else {
		embeddingJSON = nil
	}

	// Handle metadata JSON - pass NULL if empty
	var metadataJSON interface{}
	if ep.Metadata != "" {
		metadataJSON = ep.Metadata
	} else {
		metadataJSON = nil
	}

	query := `
		INSERT INTO episodes (
			id, content, name, source, source_model, source_description,
			group_id, tags, embedding, created_at, valid_at, expired_at, metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.ExecContext(ctx, query,
		ep.ID, ep.Content, ep.Name, ep.Source, ep.SourceModel, ep.SourceDescription,
		ep.GroupID, tagsJSON, embeddingJSON, ep.CreatedAt, ep.ValidAt, ep.ExpiredAt, metadataJSON,
	)

	if err != nil {
		return fmt.Errorf("failed to insert episode: %w", err)
	}

	return nil
}

// Search finds episodes matching the given parameters
func (s *Store) Search(ctx context.Context, params models.SearchParams) ([]models.Episode, error) {
	var conditions []string
	var args []interface{}
	argIdx := 1

	// Base query - includes embedding for potential similarity calculation
	query := `
		SELECT id, content, name, source, source_model, source_description,
		       group_id, tags, embedding, created_at, valid_at, expired_at, metadata
		FROM episodes
		WHERE 1=1
	`

	// Filter out episodes without embeddings if we have a query embedding
	if params.Query != "" {
		conditions = append(conditions, "embedding IS NOT NULL")
	}

	// Group filter
	if params.GroupID != "" {
		conditions = append(conditions, fmt.Sprintf("group_id = $%d", argIdx))
		args = append(args, params.GroupID)
		argIdx++
	}

	// Temporal filters
	if params.Before != nil {
		conditions = append(conditions, fmt.Sprintf("created_at < $%d", argIdx))
		args = append(args, *params.Before)
		argIdx++
	}
	if params.After != nil {
		conditions = append(conditions, fmt.Sprintf("created_at > $%d", argIdx))
		args = append(args, *params.After)
		argIdx++
	}

	// Expired filter
	if !params.IncludeExpired {
		conditions = append(conditions, "(expired_at IS NULL OR expired_at > CURRENT_TIMESTAMP)")
	}

	// Source filter
	if params.Source != "" {
		conditions = append(conditions, fmt.Sprintf("source = $%d", argIdx))
		args = append(args, params.Source)
		argIdx++
	}

	// Tags filter (all tags must be present)
	if len(params.Tags) > 0 {
		for _, tag := range params.Tags {
			conditions = append(conditions, fmt.Sprintf("list_contains(tags, $%d)", argIdx))
			args = append(args, tag)
			argIdx++
		}
	}

	// Add conditions to query
	if len(conditions) > 0 {
		query += " AND " + strings.Join(conditions, " AND ")
	}

	// Order by semantic similarity if we have a query embedding, otherwise by created_at
	if len(params.QueryEmbedding) > 0 {
		// Convert embedding to JSON array format for DuckDB
		embeddingJSON, err := json.Marshal(params.QueryEmbedding)
		if err != nil {
			// Fall back to temporal ordering if embedding conversion fails
			fmt.Fprintf(os.Stderr, "Warning: Failed to marshal query embedding: %v\n", err)
			query += " ORDER BY created_at DESC"
		} else {
			// Use VSS array_cosine_similarity for semantic ranking
			// Higher similarity scores rank first (DESC order)
			query += fmt.Sprintf(" ORDER BY array_cosine_similarity(embedding, %s::FLOAT[768]) DESC", string(embeddingJSON))
		}
	} else {
		// Fall back to temporal ordering when no query embedding available
		query += " ORDER BY created_at DESC"
	}

	// Limit results
	if params.MaxResults > 0 {
		query += fmt.Sprintf(" LIMIT %d", params.MaxResults)
	} else {
		query += " LIMIT 10"
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search query: %w", err)
	}
	defer rows.Close()

	return s.scanEpisodes(rows)
}

// GetEpisode retrieves a single episode by ID
func (s *Store) GetEpisode(ctx context.Context, id string) (*models.Episode, error) {
	query := `
		SELECT id, content, name, source, source_model, source_description,
		       group_id, tags, embedding, created_at, valid_at, expired_at, metadata
		FROM episodes
		WHERE id = ?
	`

	row := s.db.QueryRowContext(ctx, query, id)
	ep, err := s.scanEpisode(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("episode not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get episode: %w", err)
	}

	return ep, nil
}

// UpdateEpisode modifies an existing episode
func (s *Store) UpdateEpisode(ctx context.Context, id string, params models.UpdateParams) error {
	var updates []string
	var args []interface{}

	// Convert tags to JSON if provided
	if params.Tags != nil {
		tagsJSON, _ := json.Marshal(*params.Tags)
		updates = append(updates, "tags = ?")
		args = append(args, string(tagsJSON))
	}

	if params.ExpiredAt != nil {
		updates = append(updates, "expired_at = ?")
		args = append(args, *params.ExpiredAt)
	}

	if params.Metadata != nil {
		updates = append(updates, "metadata = ?")
		args = append(args, *params.Metadata)
	}

	if len(updates) == 0 {
		return fmt.Errorf("no updates provided")
	}

	args = append(args, id)
	query := fmt.Sprintf("UPDATE episodes SET %s WHERE id = ?", strings.Join(updates, ", "))

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update episode: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("episode not found: %s", id)
	}

	return nil
}

// DeleteEpisode removes an episode from the store
func (s *Store) DeleteEpisode(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM episodes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete episode: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("episode not found: %s", id)
	}

	return nil
}

// Close closes the database connection
func (s *Store) Close() error {
	return s.db.Close()
}

// Helper functions for scanning rows

func (s *Store) scanEpisode(row *sql.Row) (*models.Episode, error) {
	var ep models.Episode
	var tagsRaw, embeddingRaw, metadataRaw interface{}

	err := row.Scan(
		&ep.ID, &ep.Content, &ep.Name, &ep.Source, &ep.SourceModel, &ep.SourceDescription,
		&ep.GroupID, &tagsRaw, &embeddingRaw, &ep.CreatedAt, &ep.ValidAt, &ep.ExpiredAt, &metadataRaw,
	)
	if err != nil {
		return nil, err
	}

	// Parse tags - DuckDB returns VARCHAR[] as []interface{}
	if tagsRaw != nil {
		switch v := tagsRaw.(type) {
		case []interface{}:
			ep.Tags = make([]string, len(v))
			for i, tag := range v {
				if s, ok := tag.(string); ok {
					ep.Tags[i] = s
				}
			}
		case []string:
			ep.Tags = v
		}
	}

	// Parse embedding - DuckDB returns FLOAT[] as []interface{} with float32 elements
	if embeddingRaw != nil {
		switch v := embeddingRaw.(type) {
		case []interface{}:
			ep.Embedding = make([]float32, len(v))
			for i, val := range v {
				if f, ok := val.(float32); ok {
					ep.Embedding[i] = f
				}
			}
		case []float32:
			ep.Embedding = v
		}
	}

	// Metadata - DuckDB returns JSON as map[string]interface{}, need to re-encode
	if metadataRaw != nil {
		switch v := metadataRaw.(type) {
		case map[string]interface{}:
			if data, err := json.Marshal(v); err == nil {
				ep.Metadata = string(data)
			}
		case string:
			ep.Metadata = v
		}
	}

	return &ep, nil
}

func (s *Store) scanEpisodes(rows *sql.Rows) ([]models.Episode, error) {
	var episodes []models.Episode

	for rows.Next() {
		var ep models.Episode
		var tagsRaw, embeddingRaw, metadataRaw interface{}

		err := rows.Scan(
			&ep.ID, &ep.Content, &ep.Name, &ep.Source, &ep.SourceModel, &ep.SourceDescription,
			&ep.GroupID, &tagsRaw, &embeddingRaw, &ep.CreatedAt, &ep.ValidAt, &ep.ExpiredAt, &metadataRaw,
		)
		if err != nil {
			return nil, err
		}

		// Parse tags - DuckDB returns VARCHAR[] as []interface{}
		if tagsRaw != nil {
			switch v := tagsRaw.(type) {
			case []interface{}:
				ep.Tags = make([]string, len(v))
				for i, tag := range v {
					if s, ok := tag.(string); ok {
						ep.Tags[i] = s
					}
				}
			case []string:
				ep.Tags = v
			}
		}

		// Parse embedding - DuckDB returns FLOAT[] as []interface{} with float32 elements
		if embeddingRaw != nil {
			switch v := embeddingRaw.(type) {
			case []interface{}:
				ep.Embedding = make([]float32, len(v))
				for i, val := range v {
					if f, ok := val.(float32); ok {
						ep.Embedding[i] = f
					}
				}
			case []float32:
				ep.Embedding = v
			}
		}

		// Metadata - DuckDB returns JSON as map[string]interface{}, need to re-encode
		if metadataRaw != nil {
			switch v := metadataRaw.(type) {
			case map[string]interface{}:
				if data, err := json.Marshal(v); err == nil {
					ep.Metadata = string(data)
				}
			case string:
				ep.Metadata = v
			}
		}

		episodes = append(episodes, ep)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return episodes, nil
}
