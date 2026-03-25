package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/google/uuid"
	"github.com/oscillatelabsllc/engram/internal/models"
)

// Store wraps DuckDB operations
type Store struct {
	db           *sql.DB
	ftsStale     bool
	ftsAvailable bool
	ftsMu        sync.Mutex
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
	// Install and load VSS extension as separate calls so the download
	// completes before LOAD attempts to use the file.
	if _, err := s.db.Exec("INSTALL vss"); err != nil {
		return fmt.Errorf("failed to install VSS extension: %w", err)
	}
	if _, err := s.db.Exec("LOAD vss"); err != nil {
		return fmt.Errorf("failed to load VSS extension: %w", err)
	}

	schema := `
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

	// Create knowledge graph tables
	graphSchema := `
		CREATE TABLE IF NOT EXISTS entities (
			id VARCHAR PRIMARY KEY,
			canonical_name TEXT NOT NULL,
			entity_type VARCHAR,
			embedding FLOAT[768],
			group_id VARCHAR DEFAULT 'default',
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			metadata JSON
		);
		CREATE INDEX IF NOT EXISTS idx_entities_group_id ON entities (group_id);
		CREATE INDEX IF NOT EXISTS idx_entities_canonical_name ON entities (canonical_name);

		CREATE TABLE IF NOT EXISTS knowledge (
			id VARCHAR PRIMARY KEY,
			subject_entity_id VARCHAR NOT NULL,
			predicate VARCHAR NOT NULL,
			object_entity_id VARCHAR NOT NULL,
			source_episode_id VARCHAR,
			source VARCHAR NOT NULL,
			group_id VARCHAR DEFAULT 'default',
			embedding FLOAT[768],
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
			expired_at TIMESTAMPTZ,
			confidence FLOAT DEFAULT 1.0,
			verified BOOLEAN DEFAULT FALSE,
			metadata JSON
		);
		CREATE INDEX IF NOT EXISTS idx_knowledge_subject ON knowledge (subject_entity_id);
		CREATE INDEX IF NOT EXISTS idx_knowledge_object ON knowledge (object_entity_id);
		CREATE INDEX IF NOT EXISTS idx_knowledge_source_episode ON knowledge (source_episode_id);
		CREATE INDEX IF NOT EXISTS idx_knowledge_group_id ON knowledge (group_id);

		CREATE TABLE IF NOT EXISTS episode_links (
			id VARCHAR PRIMARY KEY,
			source_episode_id VARCHAR NOT NULL,
			target_episode_id VARCHAR NOT NULL,
			relationship VARCHAR NOT NULL,
			via_entity_id VARCHAR,
			weight FLOAT DEFAULT 1.0,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_episode_links_source ON episode_links (source_episode_id);
		CREATE INDEX IF NOT EXISTS idx_episode_links_target ON episode_links (target_episode_id);
	`
	if _, err := s.db.Exec(graphSchema); err != nil {
		return fmt.Errorf("failed to create graph schema: %w", err)
	}

	// Run migrations for existing databases
	if err := s.migrate(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Try to create HNSW index (will fail if already exists, which is fine)
	// Note: VSS extension syntax may vary, this is a placeholder
	_, _ = s.db.Exec("CREATE INDEX IF NOT EXISTS idx_episodes_embedding ON episodes USING HNSW (embedding)")

	// Install and load FTS extension for full-text search.
	// INSTALL downloads the extension; LOAD may fail if the file isn't flushed yet
	// (observed in CI), so we retry LOAD once after a brief pause.
	// Non-fatal: keyword/hybrid search degrade gracefully if FTS is unavailable.
	if _, err := s.db.Exec("INSTALL fts"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: FTS extension install failed (keyword/hybrid search unavailable): %v\n", err)
	} else if err := s.loadFTSWithRetry(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: FTS extension load failed (keyword/hybrid search unavailable): %v\n", err)
	} else {
		s.ftsAvailable = true
		s.ftsStale = true
	}

	return nil
}

// loadFTSWithRetry attempts to LOAD the FTS extension, retrying once after a
// short pause if the first attempt fails. This handles a race condition where
// INSTALL downloads the file but it isn't flushed to disk before LOAD runs.
func (s *Store) loadFTSWithRetry() error {
	_, err := s.db.Exec("LOAD fts")
	if err == nil {
		return nil
	}
	time.Sleep(100 * time.Millisecond)
	_, err = s.db.Exec("LOAD fts")
	return err
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

	s.ftsMu.Lock()
	s.ftsStale = true
	s.ftsMu.Unlock()

	return nil
}

// episodeCols is the standard column list for episode queries.
const episodeCols = `id, content, name, source, source_model, source_description,
	group_id, tags, created_at, valid_at, expired_at, metadata`

// Search finds episodes matching the given parameters
func (s *Store) Search(ctx context.Context, params models.SearchParams) ([]models.Episode, error) {
	// Determine effective search mode
	mode := params.SearchMode
	if mode == "" {
		mode = "vector"
	}

	needsFTS := mode == "keyword" || mode == "hybrid"

	// If FTS is needed but unavailable, degrade gracefully
	if needsFTS && !s.ftsAvailable {
		fmt.Fprintf(os.Stderr, "Warning: %s search requested but FTS extension unavailable, falling back to vector mode\n", mode)
		mode = "vector"
		needsFTS = false
	}

	// Warn if min_similarity is set but won't be applied
	if params.MinSimilarity > 0 && mode != "vector" && mode != "" {
		fmt.Fprintf(os.Stderr, "Warning: min_similarity is ignored in %q search mode (only applies to vector mode)\n", mode)
	}

	// Rebuild FTS index if needed for keyword/hybrid modes
	if needsFTS && params.Query != "" {
		if err := s.ensureFTSIndex(); err != nil {
			return nil, fmt.Errorf("failed to ensure FTS index: %w", err)
		}
	}

	var conditions []string
	var args []interface{}
	argIdx := 1
	hasSemantic := len(params.QueryEmbedding) > 0

	var embeddingJSON []byte
	if hasSemantic {
		var err error
		embeddingJSON, err = json.Marshal(params.QueryEmbedding)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to marshal query embedding: %v\n", err)
			hasSemantic = false
		}
	}

	// For keyword mode, we never use semantic similarity
	if mode == "keyword" {
		hasSemantic = false
	}

	// Handle tag boost: build computed column with bind params before other conditions
	hasTagBoost := len(params.Tags) > 0 && params.TagBoost > 0
	var tagBoostExpr string
	if hasTagBoost {
		var tagChecks []string
		for _, tag := range params.Tags {
			tagChecks = append(tagChecks, fmt.Sprintf("CAST(list_contains(tags, $%d) AS INTEGER)", argIdx))
			args = append(args, tag)
			argIdx++
		}
		tagBoostExpr = fmt.Sprintf("(%s) * 1.0 / %d.0", strings.Join(tagChecks, " + "), len(params.Tags))
	}

	// Build the computed columns (similarity, bm25) based on mode
	hasBM25 := needsFTS && params.Query != ""
	var computedCols string
	switch {
	case hasSemantic && hasBM25:
		computedCols = fmt.Sprintf(`,
			array_cosine_similarity(embedding, %s::FLOAT[768]) AS similarity,
			fts_main_episodes.match_bm25(id, '%s', fields := 'content,name') AS bm25_score`,
			string(embeddingJSON), sanitizeFTSQuery(params.Query))
	case hasSemantic:
		computedCols = fmt.Sprintf(`,
			array_cosine_similarity(embedding, %s::FLOAT[768]) AS similarity`,
			string(embeddingJSON))
	case hasBM25:
		computedCols = fmt.Sprintf(`,
			NULL AS similarity,
			fts_main_episodes.match_bm25(id, '%s', fields := 'content,name') AS bm25_score`,
			sanitizeFTSQuery(params.Query))
	default:
		computedCols = `,
			NULL AS similarity`
	}

	// Add tag_match_ratio as computed column when boosting
	if hasTagBoost {
		computedCols += fmt.Sprintf(", %s AS tag_match_ratio", tagBoostExpr)
	}

	innerSelect := fmt.Sprintf("SELECT %s%s FROM episodes WHERE 1=1", episodeCols, computedCols)

	// Only filter out NULL embeddings when we're actually doing semantic ranking
	if hasSemantic {
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

	// Tags filter: hard AND when TagBoost=0, skip when boosting (handled as computed column)
	if len(params.Tags) > 0 && !hasTagBoost {
		for _, tag := range params.Tags {
			conditions = append(conditions, fmt.Sprintf("list_contains(tags, $%d)", argIdx))
			args = append(args, tag)
			argIdx++
		}
	}

	// Add conditions to inner query
	if len(conditions) > 0 {
		innerSelect += " AND " + strings.Join(conditions, " AND ")
	}

	// Tag boost addend for ORDER BY / relevance computation
	tagBoostAddend := ""
	if hasTagBoost {
		tagBoostAddend = fmt.Sprintf(" + %f * COALESCE(s.tag_match_ratio, 0.0)", params.TagBoost)
	}

	// Build the final query based on mode
	// All paths return: episodeCols, similarity, relevance (14 columns for scanEpisodes)
	var query string
	switch {
	case mode == "keyword" && hasBM25:
		query = fmt.Sprintf(`WITH scored AS (%s),
			bm25_stats AS (
				SELECT MIN(bm25_score) AS min_bm25, MAX(bm25_score) AS max_bm25
				FROM scored WHERE bm25_score IS NOT NULL
			)
			SELECT %s, s.similarity,
			       CASE WHEN b.max_bm25 = b.min_bm25 THEN 1.0
			            WHEN s.bm25_score IS NOT NULL THEN
			                (s.bm25_score - b.min_bm25) / (b.max_bm25 - b.min_bm25)
			            ELSE 0.0 END%s AS relevance
			FROM scored s, bm25_stats b
			WHERE s.bm25_score IS NOT NULL
			ORDER BY relevance DESC`,
			innerSelect, episodeCols, tagBoostAddend)

	case mode == "hybrid" && hasBM25:
		// Default alpha to 0.7 when not explicitly set (Go zero-value).
		alpha := params.SearchAlpha
		if alpha == 0 {
			alpha = 0.7
		}

		if hasSemantic {
			query = fmt.Sprintf(`WITH scored AS (%s),
				bm25_stats AS (
					SELECT MIN(bm25_score) AS min_bm25, MAX(bm25_score) AS max_bm25
					FROM scored WHERE bm25_score IS NOT NULL
				),
				cosine_stats AS (
					SELECT MIN(similarity) AS min_cos, MAX(similarity) AS max_cos
					FROM scored WHERE similarity IS NOT NULL
				),
				hybrid AS (
					SELECT s.*,
					       CASE
					           WHEN b.max_bm25 = b.min_bm25 THEN
					               CASE WHEN s.bm25_score IS NOT NULL THEN 1.0 ELSE 0.0 END
					           WHEN s.bm25_score IS NOT NULL THEN
					               (s.bm25_score - b.min_bm25) / (b.max_bm25 - b.min_bm25)
					           ELSE 0.0
					       END AS norm_bm25,
					       CASE
					           WHEN c.max_cos = c.min_cos THEN
					               CASE WHEN s.similarity IS NOT NULL THEN 1.0 ELSE 0.0 END
					           WHEN s.similarity IS NOT NULL THEN
					               (s.similarity - c.min_cos) / (c.max_cos - c.min_cos)
					           ELSE 0.0
					       END AS norm_cosine
					FROM scored s, bm25_stats b, cosine_stats c
					WHERE s.bm25_score IS NOT NULL OR s.similarity IS NOT NULL
				)
				SELECT %s, similarity,
				       (%f * norm_cosine + %f * norm_bm25)%s AS relevance
				FROM hybrid s
				ORDER BY relevance DESC`,
				innerSelect, episodeCols, alpha, 1.0-alpha, tagBoostAddend)
		} else {
			// No embedding — hybrid degrades to keyword
			query = fmt.Sprintf(`WITH scored AS (%s),
				bm25_stats AS (
					SELECT MIN(bm25_score) AS min_bm25, MAX(bm25_score) AS max_bm25
					FROM scored WHERE bm25_score IS NOT NULL
				)
				SELECT %s, s.similarity,
				       CASE WHEN b.max_bm25 = b.min_bm25 THEN 1.0
				            WHEN s.bm25_score IS NOT NULL THEN
				                (s.bm25_score - b.min_bm25) / (b.max_bm25 - b.min_bm25)
				            ELSE 0.0 END%s AS relevance
				FROM scored s, bm25_stats b
				WHERE s.bm25_score IS NOT NULL
				ORDER BY relevance DESC`,
				innerSelect, episodeCols, tagBoostAddend)
		}

	default: // vector mode, or any mode without a query
		if hasSemantic {
			query = fmt.Sprintf(`WITH scored AS (%s),
				cosine_stats AS (
					SELECT MIN(similarity) AS min_cos, MAX(similarity) AS max_cos
					FROM scored WHERE similarity IS NOT NULL
				)
				SELECT %s, s.similarity,
				       CASE WHEN c.max_cos = c.min_cos THEN
				               CASE WHEN s.similarity IS NOT NULL THEN 1.0 ELSE NULL END
				            WHEN s.similarity IS NOT NULL THEN
				               (s.similarity - c.min_cos) / (c.max_cos - c.min_cos)
				            ELSE NULL END%s AS relevance
				FROM scored s, cosine_stats c`,
				innerSelect, episodeCols, tagBoostAddend)

			if params.MinSimilarity > 0 {
				query += fmt.Sprintf(" WHERE s.similarity >= %f", params.MinSimilarity)
			}
			query += " ORDER BY relevance DESC"
		} else {
			query = fmt.Sprintf("WITH scored AS (%s) SELECT %s, s.similarity, NULL AS relevance FROM scored s ORDER BY s.created_at DESC",
				innerSelect, episodeCols)
		}
	}

	// Apply LIMIT
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

	episodes, err := s.scanEpisodes(rows)
	if err != nil {
		return nil, err
	}

	// Keyword fallback: BM25 cannot index pure numeric tokens (DuckDB FTS limitation).
	// When keyword mode returns no results and the query is non-empty, fall back to
	// ILIKE content search to catch account IDs, ticket numbers, and other identifiers.
	if len(episodes) == 0 && mode == "keyword" && params.Query != "" {
		episodes, err = s.contentFallbackSearch(ctx, params)
		if err != nil {
			return nil, err
		}
	}

	return episodes, nil
}

// contentFallbackSearch performs an ILIKE content search as a fallback when BM25
// cannot match the query (e.g. pure numeric tokens). Returns results with similarity
// nil and relevance hardcoded to 1.0 — ILIKE is a binary match with no ranking signal,
// so all fallback results are treated as equally relevant.
func (s *Store) contentFallbackSearch(ctx context.Context, params models.SearchParams) ([]models.Episode, error) {
	var conditions []string
	var args []interface{}
	argIdx := 1

	// ILIKE match on content and name
	conditions = append(conditions, fmt.Sprintf("(content ILIKE $%d OR name ILIKE $%d)", argIdx, argIdx))
	args = append(args, "%"+params.Query+"%")
	argIdx++

	// Apply the same filters as the main search
	if params.GroupID != "" {
		conditions = append(conditions, fmt.Sprintf("group_id = $%d", argIdx))
		args = append(args, params.GroupID)
		argIdx++
	}
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
	if !params.IncludeExpired {
		conditions = append(conditions, "(expired_at IS NULL OR expired_at > CURRENT_TIMESTAMP)")
	}
	if params.Source != "" {
		conditions = append(conditions, fmt.Sprintf("source = $%d", argIdx))
		args = append(args, params.Source)
		argIdx++
	}
	if len(params.Tags) > 0 {
		for _, tag := range params.Tags {
			conditions = append(conditions, fmt.Sprintf("list_contains(tags, $%d)", argIdx))
			args = append(args, tag)
			argIdx++
		}
	}

	where := strings.Join(conditions, " AND ")
	limit := params.MaxResults
	if limit <= 0 {
		limit = 10
	}

	query := fmt.Sprintf(
		"SELECT %s, NULL AS similarity, 1.0 AS relevance FROM episodes WHERE %s ORDER BY created_at DESC LIMIT %d",
		episodeCols, where, limit,
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute fallback search: %w", err)
	}
	defer rows.Close()
	return s.scanEpisodes(rows)
}

// GetEpisode retrieves a single episode by ID
func (s *Store) GetEpisode(ctx context.Context, id string) (*models.Episode, error) {
	query := `
		SELECT id, content, name, source, source_model, source_description,
		       group_id, tags, created_at, valid_at, expired_at, metadata
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

	s.ftsMu.Lock()
	s.ftsStale = true
	s.ftsMu.Unlock()

	return nil
}

// CountEpisodes returns the total number of non-expired episodes
func (s *Store) CountEpisodes(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM episodes WHERE expired_at IS NULL OR expired_at > CURRENT_TIMESTAMP",
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count episodes: %w", err)
	}
	return count, nil
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

	s.ftsMu.Lock()
	s.ftsStale = true
	s.ftsMu.Unlock()

	return nil
}

// rebuildFTSIndex rebuilds the full-text search index on the episodes table.
// Must be called with ftsMu held.
//
// Scaling note: This does a full rebuild (drop + re-index) because DuckDB FTS
// doesn't support incremental updates. Cost is O(rows × avg_text_length):
//   - <1K episodes: sub-second
//   - 1K–10K: 1–5s (noticeable on first search after a write)
//   - 10K+: consider alternative indexing strategy
func (s *Store) rebuildFTSIndex() error {
	_, err := s.db.Exec("PRAGMA create_fts_index('episodes', 'id', 'content', 'name', overwrite=1)")
	if err != nil {
		return fmt.Errorf("failed to rebuild FTS index: %w", err)
	}
	return nil
}

// ensureFTSIndex rebuilds the FTS index if it is stale.
func (s *Store) ensureFTSIndex() error {
	s.ftsMu.Lock()
	defer s.ftsMu.Unlock()

	if !s.ftsStale {
		return nil
	}

	if err := s.rebuildFTSIndex(); err != nil {
		return err
	}
	s.ftsStale = false
	return nil
}

// InsertEntity adds a new entity to the store, or returns the existing entity ID if a
// semantically matching entity already exists (cosine similarity >= threshold).
func (s *Store) InsertEntity(ctx context.Context, entity *models.Entity, similarityThreshold float64) (*models.Entity, error) {
	if similarityThreshold <= 0 {
		similarityThreshold = 0.88
	}

	// If we have an embedding, check for an existing matching entity
	if len(entity.Embedding) > 0 {
		embJSON, _ := json.Marshal(entity.Embedding)
		groupFilter := "default"
		if entity.GroupID != "" {
			groupFilter = entity.GroupID
		}

		var existingID, existingName, existingType string
		var existingCreatedAt time.Time
		err := s.db.QueryRowContext(ctx, `
			SELECT id, canonical_name, entity_type, created_at
			FROM entities
			WHERE embedding IS NOT NULL
			  AND group_id = ?
			  AND array_cosine_similarity(embedding, `+string(embJSON)+`::FLOAT[768]) >= ?
			ORDER BY array_cosine_similarity(embedding, `+string(embJSON)+`::FLOAT[768]) DESC
			LIMIT 1
		`, groupFilter, similarityThreshold).Scan(&existingID, &existingName, &existingType, &existingCreatedAt)

		if err == nil {
			// Found a matching entity — return it
			return &models.Entity{
				ID:            existingID,
				CanonicalName: existingName,
				EntityType:    existingType,
				GroupID:       groupFilter,
				CreatedAt:     existingCreatedAt,
			}, nil
		}
		// sql.ErrNoRows means no match — fall through to insert
		if err != sql.ErrNoRows {
			return nil, fmt.Errorf("failed to search for matching entity: %w", err)
		}
	}

	// No match found — insert new entity
	if entity.ID == "" {
		entity.ID = uuid.New().String()
	}
	if entity.CreatedAt.IsZero() {
		entity.CreatedAt = time.Now()
	}
	if entity.GroupID == "" {
		entity.GroupID = "default"
	}

	var embeddingJSON interface{}
	if len(entity.Embedding) > 0 {
		data, _ := json.Marshal(entity.Embedding)
		embeddingJSON = string(data)
	}

	var metadataJSON interface{}
	if entity.Metadata != "" {
		metadataJSON = entity.Metadata
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO entities (id, canonical_name, entity_type, embedding, group_id, created_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, entity.ID, entity.CanonicalName, entity.EntityType, embeddingJSON, entity.GroupID, entity.CreatedAt, metadataJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to insert entity: %w", err)
	}

	return entity, nil
}

// GetEntity retrieves a single entity by ID
func (s *Store) GetEntity(ctx context.Context, id string) (*models.Entity, error) {
	var entity models.Entity
	var metadataRaw interface{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, canonical_name, entity_type, group_id, created_at, metadata
		FROM entities WHERE id = ?
	`, id).Scan(&entity.ID, &entity.CanonicalName, &entity.EntityType, &entity.GroupID, &entity.CreatedAt, &metadataRaw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("entity not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get entity: %w", err)
	}
	if metadataRaw != nil {
		switch v := metadataRaw.(type) {
		case map[string]interface{}:
			if data, err := json.Marshal(v); err == nil {
				entity.Metadata = string(data)
			}
		case string:
			entity.Metadata = v
		}
	}
	return &entity, nil
}

// InsertKnowledgeTriple adds a knowledge triple to the store
func (s *Store) InsertKnowledgeTriple(ctx context.Context, triple *models.KnowledgeTriple) error {
	if triple.ID == "" {
		triple.ID = uuid.New().String()
	}
	if triple.CreatedAt.IsZero() {
		triple.CreatedAt = time.Now()
	}
	if triple.GroupID == "" {
		triple.GroupID = "default"
	}
	if triple.Confidence == 0 {
		triple.Confidence = 1.0
	}

	var embeddingJSON interface{}
	if len(triple.Embedding) > 0 {
		data, _ := json.Marshal(triple.Embedding)
		embeddingJSON = string(data)
	}

	var metadataJSON interface{}
	if triple.Metadata != "" {
		metadataJSON = triple.Metadata
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge (
			id, subject_entity_id, predicate, object_entity_id,
			source_episode_id, source, group_id, embedding,
			created_at, expired_at, confidence, verified, metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		triple.ID, triple.SubjectEntityID, triple.Predicate, triple.ObjectEntityID,
		triple.SourceEpisodeID, triple.Source, triple.GroupID, embeddingJSON,
		triple.CreatedAt, triple.ExpiredAt, triple.Confidence, triple.Verified, metadataJSON,
	)
	if err != nil {
		return fmt.Errorf("failed to insert knowledge triple: %w", err)
	}
	return nil
}

// SearchKnowledge finds knowledge triples matching the given query embedding
func (s *Store) SearchKnowledge(ctx context.Context, queryEmbedding []float32, groupID string, maxResults int, minSimilarity float64) ([]models.KnowledgeTriple, error) {
	if maxResults <= 0 {
		maxResults = 10
	}
	if minSimilarity <= 0 {
		minSimilarity = 0.35
	}

	embJSON, err := json.Marshal(queryEmbedding)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query embedding: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT k.id, k.subject_entity_id, k.predicate, k.object_entity_id,
		       k.source_episode_id, k.source, k.group_id, k.created_at,
		       k.expired_at, k.confidence, k.verified, k.metadata,
		       se.canonical_name AS subject_name, oe.canonical_name AS object_name,
		       array_cosine_similarity(k.embedding, %s::FLOAT[768]) AS similarity
		FROM knowledge k
		JOIN entities se ON k.subject_entity_id = se.id
		JOIN entities oe ON k.object_entity_id = oe.id
		WHERE k.embedding IS NOT NULL
		  AND (k.expired_at IS NULL OR k.expired_at > CURRENT_TIMESTAMP)
		  AND array_cosine_similarity(k.embedding, %s::FLOAT[768]) >= %f
	`, string(embJSON), string(embJSON), minSimilarity)

	if groupID != "" {
		query += fmt.Sprintf(" AND k.group_id = '%s'", groupID)
	}

	query += fmt.Sprintf(" ORDER BY similarity DESC LIMIT %d", maxResults)

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to search knowledge: %w", err)
	}
	defer rows.Close()

	var triples []models.KnowledgeTriple
	for rows.Next() {
		var t models.KnowledgeTriple
		var metadataRaw interface{}
		var similarity sql.NullFloat64
		err := rows.Scan(
			&t.ID, &t.SubjectEntityID, &t.Predicate, &t.ObjectEntityID,
			&t.SourceEpisodeID, &t.Source, &t.GroupID, &t.CreatedAt,
			&t.ExpiredAt, &t.Confidence, &t.Verified, &metadataRaw,
			&t.SubjectName, &t.ObjectName, &similarity,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan knowledge triple: %w", err)
		}
		if metadataRaw != nil {
			switch v := metadataRaw.(type) {
			case map[string]interface{}:
				if data, err := json.Marshal(v); err == nil {
					t.Metadata = string(data)
				}
			case string:
				t.Metadata = v
			}
		}
		triples = append(triples, t)
	}
	return triples, rows.Err()
}

// InsertEpisodeLink adds a link between two episodes
func (s *Store) InsertEpisodeLink(ctx context.Context, link *models.EpisodeLink) error {
	if link.ID == "" {
		link.ID = uuid.New().String()
	}
	if link.CreatedAt.IsZero() {
		link.CreatedAt = time.Now()
	}
	if link.Weight == 0 {
		link.Weight = 1.0
	}

	// Check for existing link (deduplicate)
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM episode_links
		WHERE source_episode_id = ? AND target_episode_id = ? AND relationship = ?
	`, link.SourceEpisodeID, link.TargetEpisodeID, link.Relationship).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check existing link: %w", err)
	}
	if exists > 0 {
		return nil // Link already exists, skip silently
	}

	var viaEntityID interface{}
	if link.ViaEntityID != "" {
		viaEntityID = link.ViaEntityID
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO episode_links (id, source_episode_id, target_episode_id, relationship, via_entity_id, weight, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, link.ID, link.SourceEpisodeID, link.TargetEpisodeID, link.Relationship, viaEntityID, link.Weight, link.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert episode link: %w", err)
	}
	return nil
}

// GetEpisodeLinks retrieves all links for a given episode (both directions)
func (s *Store) GetEpisodeLinks(ctx context.Context, episodeID string) ([]models.EpisodeLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source_episode_id, target_episode_id, relationship, via_entity_id, weight, created_at
		FROM episode_links
		WHERE source_episode_id = ? OR target_episode_id = ?
		ORDER BY created_at DESC
	`, episodeID, episodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get episode links: %w", err)
	}
	defer rows.Close()

	var links []models.EpisodeLink
	for rows.Next() {
		var link models.EpisodeLink
		var viaEntityID sql.NullString
		err := rows.Scan(&link.ID, &link.SourceEpisodeID, &link.TargetEpisodeID,
			&link.Relationship, &viaEntityID, &link.Weight, &link.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan episode link: %w", err)
		}
		if viaEntityID.Valid {
			link.ViaEntityID = viaEntityID.String
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

// Close closes the database connection
func (s *Store) Close() error {
	return s.db.Close()
}

// sanitizeFTSQuery escapes a user query for safe use in DuckDB's match_bm25().
// The result is interpolated into a SQL string literal ('...'), so single quotes
// are escaped. FTS operators and SQL metacharacters are stripped as defense-in-depth.
//
// Note: single-quote escaping ('' inside a SQL string) is the primary injection
// defense — it prevents breaking out of the string literal. The other stripping
// is belt-and-suspenders.
func sanitizeFTSQuery(s string) string {
	// Remove multi-char SQL metacharacters first (before individual char stripping)
	s = strings.ReplaceAll(s, "--", "")
	s = strings.ReplaceAll(s, "/*", "")
	s = strings.ReplaceAll(s, "*/", "")
	// Strip FTS query syntax and SQL metacharacters
	replacer := strings.NewReplacer(
		`\`, ``,
		`"`, ``,
		`+`, ``,
		`-`, ``,
		`*`, ``,
		`(`, ``,
		`)`, ``,
		`;`, ``,
	)
	s = replacer.Replace(s)
	// Escape single quotes for SQL string literal safety
	return strings.ReplaceAll(s, "'", "''")
}

// Helper functions for scanning rows

func (s *Store) scanEpisode(row *sql.Row) (*models.Episode, error) {
	var ep models.Episode
	var tagsRaw, metadataRaw interface{}

	err := row.Scan(
		&ep.ID, &ep.Content, &ep.Name, &ep.Source, &ep.SourceModel, &ep.SourceDescription,
		&ep.GroupID, &tagsRaw, &ep.CreatedAt, &ep.ValidAt, &ep.ExpiredAt, &metadataRaw,
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
		var tagsRaw, metadataRaw interface{}
		var similarity, relevance sql.NullFloat64

		err := rows.Scan(
			&ep.ID, &ep.Content, &ep.Name, &ep.Source, &ep.SourceModel, &ep.SourceDescription,
			&ep.GroupID, &tagsRaw, &ep.CreatedAt, &ep.ValidAt, &ep.ExpiredAt, &metadataRaw,
			&similarity, &relevance,
		)
		if err != nil {
			return nil, err
		}
		if similarity.Valid {
			ep.Similarity = &similarity.Float64
		}
		if relevance.Valid {
			ep.Relevance = &relevance.Float64
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
