package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/oscillatelabsllc/engram/internal/models"
)

// ListUnenrichedEpisodes returns episodes the dreamer has not yet processed,
// oldest first, up to limit rows.
func (s *Store) ListUnenrichedEpisodes(ctx context.Context, limit int) ([]models.Episode, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, content, name, source, group_id, created_at, metadata
		FROM episodes
		WHERE enriched_at IS NULL
		ORDER BY created_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list unenriched episodes: %w", err)
	}
	defer rows.Close()

	var episodes []models.Episode
	for rows.Next() {
		var ep models.Episode
		var metadataRaw interface{}
		if err := rows.Scan(&ep.ID, &ep.Content, &ep.Name, &ep.Source, &ep.GroupID, &ep.CreatedAt, &metadataRaw); err != nil {
			return nil, fmt.Errorf("failed to scan unenriched episode: %w", err)
		}
		ep.Metadata = metadataToString(metadataRaw)
		episodes = append(episodes, ep)
	}
	return episodes, rows.Err()
}

// CountUnenrichedEpisodes returns how many episodes the dreamer has yet to process
func (s *Store) CountUnenrichedEpisodes(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM episodes WHERE enriched_at IS NULL").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count unenriched episodes: %w", err)
	}
	return count, nil
}

// MarkEpisodeEnriched stamps enriched_at on an episode. When enrichmentErr is
// non-empty it is merged into the episode's metadata under "enrichment_error"
// (preserving existing keys) so poison episodes are stamped and never retried.
func (s *Store) MarkEpisodeEnriched(ctx context.Context, id string, enrichmentErr string) error {
	var result sql.Result
	var err error

	if enrichmentErr == "" {
		result, err = s.db.ExecContext(ctx,
			"UPDATE episodes SET enriched_at = CURRENT_TIMESTAMP WHERE id = ?", id)
	} else {
		var metadataRaw interface{}
		if scanErr := s.db.QueryRowContext(ctx,
			"SELECT metadata FROM episodes WHERE id = ?", id).Scan(&metadataRaw); scanErr != nil {
			if scanErr == sql.ErrNoRows {
				return fmt.Errorf("episode not found: %s", id)
			}
			return fmt.Errorf("failed to read episode metadata: %w", scanErr)
		}

		meta := map[string]interface{}{}
		if existing := metadataToString(metadataRaw); existing != "" {
			// Best effort: an unparseable metadata blob is replaced rather
			// than blocking the enrichment stamp
			_ = json.Unmarshal([]byte(existing), &meta)
		}
		meta["enrichment_error"] = enrichmentErr
		merged, marshalErr := json.Marshal(meta)
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal merged metadata: %w", marshalErr)
		}

		result, err = s.db.ExecContext(ctx,
			"UPDATE episodes SET enriched_at = CURRENT_TIMESTAMP, metadata = ? WHERE id = ?",
			string(merged), id)
	}

	if err != nil {
		return fmt.Errorf("failed to mark episode enriched: %w", err)
	}
	if n, raErr := result.RowsAffected(); raErr == nil && n == 0 {
		return fmt.Errorf("episode not found: %s", id)
	}
	return nil
}

// FindEpisodesSharingEntities returns the IDs of episodes (other than
// excludeEpisodeID) whose knowledge triples reference any of the given entities.
func (s *Store) FindEpisodesSharingEntities(ctx context.Context, entityIDs []string, excludeEpisodeID string) ([]string, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(entityIDs)), ",")
	args := make([]interface{}, 0, 2*len(entityIDs)+1)
	for _, id := range entityIDs {
		args = append(args, id)
	}
	for _, id := range entityIDs {
		args = append(args, id)
	}
	args = append(args, excludeEpisodeID)

	query := fmt.Sprintf(`
		SELECT DISTINCT source_episode_id
		FROM knowledge
		WHERE (subject_entity_id IN (%s) OR object_entity_id IN (%s))
		  AND source_episode_id IS NOT NULL
		  AND source_episode_id != ''
		  AND source_episode_id != ?
	`, placeholders, placeholders)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to find episodes sharing entities: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan episode id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// metadataToString normalizes DuckDB JSON column values (returned as either
// map[string]interface{} or string) to a JSON string.
func metadataToString(raw interface{}) string {
	switch v := raw.(type) {
	case map[string]interface{}:
		if data, err := json.Marshal(v); err == nil {
			return string(data)
		}
	case string:
		return v
	}
	return ""
}
