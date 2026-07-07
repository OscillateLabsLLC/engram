package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ReembedItem is a row whose embedding needs (re)generation
type ReembedItem struct {
	ID   string
	Text string
}

// StaleEmbeddingCounts reports rows per table whose embedding is missing or
// was produced by a different model than the one currently configured
type StaleEmbeddingCounts struct {
	Episodes  int `json:"episodes"`
	Entities  int `json:"entities"`
	Knowledge int `json:"knowledge"`
}

// Total returns the sum across all tables
func (c StaleEmbeddingCounts) Total() int {
	return c.Episodes + c.Entities + c.Knowledge
}

// stalePredicate matches rows with no embedding or an embedding produced by
// a different model. IS DISTINCT FROM treats NULL embedding_model (legacy
// rows) as stale.
const stalePredicate = "(embedding IS NULL OR embedding_model IS DISTINCT FROM ?)"

// livePredicate excludes expired rows: re-embedding them wastes work, and an
// expired row that can never embed (e.g. content over the model's context
// window) would otherwise keep the stale count from reaching zero.
const livePredicate = "(expired_at IS NULL OR expired_at > CURRENT_TIMESTAMP)"

// CountReembedTargets counts rows the re-embed pass would touch. With force,
// every live row counts; otherwise only live stale rows (see stalePredicate).
func (s *Store) CountReembedTargets(ctx context.Context, model string, force bool) (StaleEmbeddingCounts, error) {
	var counts StaleEmbeddingCounts
	for _, t := range []struct {
		table string
		live  string // empty for tables without expiry
		dest  *int
	}{
		{"episodes", livePredicate, &counts.Episodes},
		{"entities", "", &counts.Entities},
		{"knowledge", livePredicate, &counts.Knowledge},
	} {
		var conds []string
		args := []interface{}{}
		if t.live != "" {
			conds = append(conds, t.live)
		}
		if !force {
			conds = append(conds, stalePredicate)
			args = append(args, model)
		}
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s", t.table)
		if len(conds) > 0 {
			query += " WHERE " + strings.Join(conds, " AND ")
		}
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(t.dest); err != nil {
			return counts, fmt.Errorf("failed to count %s re-embed targets: %w", t.table, err)
		}
	}
	return counts, nil
}

// listForReembed pages through re-embed targets using keyset pagination on id
// so rows re-stamped mid-run (or rows that keep failing) are never revisited
// within a single pass.
func (s *Store) listForReembed(ctx context.Context, query string, model string, afterID string, limit int, force bool) ([]ReembedItem, error) {
	args := []interface{}{afterID}
	if !force {
		args = append(args, model)
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ReembedItem
	for rows.Next() {
		var it ReembedItem
		if err := rows.Scan(&it.ID, &it.Text); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// ListEpisodesForReembed returns the next batch of episodes to re-embed,
// ordered by id, starting after afterID. The Text field is the episode content.
func (s *Store) ListEpisodesForReembed(ctx context.Context, model string, afterID string, limit int, force bool) ([]ReembedItem, error) {
	query := "SELECT id, content FROM episodes WHERE id > ? AND " + livePredicate
	if !force {
		query += " AND " + stalePredicate
	}
	query += " ORDER BY id LIMIT ?"
	items, err := s.listForReembed(ctx, query, model, afterID, limit, force)
	if err != nil {
		return nil, fmt.Errorf("failed to list episodes for re-embed: %w", err)
	}
	return items, nil
}

// ListEntitiesForReembed returns the next batch of entities to re-embed.
// The Text field is the canonical name (matching the original embed input).
func (s *Store) ListEntitiesForReembed(ctx context.Context, model string, afterID string, limit int, force bool) ([]ReembedItem, error) {
	query := "SELECT id, canonical_name FROM entities WHERE id > ?"
	if !force {
		query += " AND " + stalePredicate
	}
	query += " ORDER BY id LIMIT ?"
	items, err := s.listForReembed(ctx, query, model, afterID, limit, force)
	if err != nil {
		return nil, fmt.Errorf("failed to list entities for re-embed: %w", err)
	}
	return items, nil
}

// ListKnowledgeForReembed returns the next batch of knowledge triples to
// re-embed. The Text field is the "subject predicate object" triple text,
// matching how handleAddKnowledge composes the original embed input.
func (s *Store) ListKnowledgeForReembed(ctx context.Context, model string, afterID string, limit int, force bool) ([]ReembedItem, error) {
	query := `
		SELECT k.id, se.canonical_name || ' ' || k.predicate || ' ' || oe.canonical_name
		FROM knowledge k
		JOIN entities se ON k.subject_entity_id = se.id
		JOIN entities oe ON k.object_entity_id = oe.id
		WHERE k.id > ?
		  AND (k.expired_at IS NULL OR k.expired_at > CURRENT_TIMESTAMP)`
	if !force {
		query += " AND (k.embedding IS NULL OR k.embedding_model IS DISTINCT FROM ?)"
	}
	query += " ORDER BY k.id LIMIT ?"
	items, err := s.listForReembed(ctx, query, model, afterID, limit, force)
	if err != nil {
		return nil, fmt.Errorf("failed to list knowledge for re-embed: %w", err)
	}
	return items, nil
}

// updateEmbedding writes a fresh vector and its provenance stamp. The table
// name is always one of the fixed internal callers below, never user input.
func (s *Store) updateEmbedding(ctx context.Context, table, id string, embedding []float32, model string) error {
	embJSON, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("failed to marshal embedding: %w", err)
	}
	query := fmt.Sprintf("UPDATE %s SET embedding = ?, embedding_model = ? WHERE id = ?", table)
	res, err := s.db.ExecContext(ctx, query, string(embJSON), model, id)
	if err != nil {
		return fmt.Errorf("failed to update %s embedding: %w", table, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("%s row not found: %s", table, id)
	}
	return nil
}

// UpdateEpisodeEmbedding replaces an episode's embedding and provenance stamp
func (s *Store) UpdateEpisodeEmbedding(ctx context.Context, id string, embedding []float32, model string) error {
	return s.updateEmbedding(ctx, "episodes", id, embedding, model)
}

// UpdateEntityEmbedding replaces an entity's embedding and provenance stamp
func (s *Store) UpdateEntityEmbedding(ctx context.Context, id string, embedding []float32, model string) error {
	return s.updateEmbedding(ctx, "entities", id, embedding, model)
}

// UpdateKnowledgeEmbedding replaces a knowledge triple's embedding and provenance stamp
func (s *Store) UpdateKnowledgeEmbedding(ctx context.Context, id string, embedding []float32, model string) error {
	return s.updateEmbedding(ctx, "knowledge", id, embedding, model)
}
