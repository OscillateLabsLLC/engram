package db

import (
	"context"
	"fmt"

	"github.com/oscillatelabsllc/engram/internal/models"
)

// DefaultDreamRecurrence is the recurrence threshold at which a quarantined
// triple is considered a "recurring dream" worth surfacing for human review
const DefaultDreamRecurrence = 3

// ListRecurringDreams returns quarantined (ungrounded, unexpired) triples the
// Dreamer keeps re-extracting from distinct episodes — ideas that recur but
// nothing has anchored. Ordered by recurrence, most persistent first.
func (s *Store) ListRecurringDreams(ctx context.Context, groupID string, minRecurrence, limit int) ([]models.KnowledgeTriple, error) {
	if minRecurrence <= 0 {
		minRecurrence = DefaultDreamRecurrence
	}
	if limit <= 0 {
		limit = 10
	}

	query := `
		SELECT k.id, k.subject_entity_id, k.predicate, k.object_entity_id,
		       k.source_episode_id, k.source, k.group_id, k.created_at,
		       k.confidence, k.verified, k.grounded, k.recurrence,
		       se.canonical_name, oe.canonical_name
		FROM knowledge k
		JOIN entities se ON k.subject_entity_id = se.id
		JOIN entities oe ON k.object_entity_id = oe.id
		WHERE k.grounded = FALSE
		  AND (k.expired_at IS NULL OR k.expired_at > CURRENT_TIMESTAMP)
		  AND k.recurrence >= ?`
	args := []interface{}{minRecurrence}
	if groupID != "" {
		query += " AND k.group_id = ?"
		args = append(args, groupID)
	}
	query += " ORDER BY k.recurrence DESC, k.confidence DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list recurring dreams: %w", err)
	}
	defer rows.Close()

	triples := []models.KnowledgeTriple{}
	for rows.Next() {
		var t models.KnowledgeTriple
		if err := rows.Scan(
			&t.ID, &t.SubjectEntityID, &t.Predicate, &t.ObjectEntityID,
			&t.SourceEpisodeID, &t.Source, &t.GroupID, &t.CreatedAt,
			&t.Confidence, &t.Verified, &t.Grounded, &t.Recurrence,
			&t.SubjectName, &t.ObjectName,
		); err != nil {
			return nil, fmt.Errorf("failed to scan recurring dream: %w", err)
		}
		triples = append(triples, t)
	}
	return triples, rows.Err()
}

// CountRecurringDreams counts quarantined triples at or above the recurrence
// threshold — the "how many things should we talk about" number
func (s *Store) CountRecurringDreams(ctx context.Context, minRecurrence int) (int, error) {
	if minRecurrence <= 0 {
		minRecurrence = DefaultDreamRecurrence
	}
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM knowledge
		WHERE grounded = FALSE
		  AND (expired_at IS NULL OR expired_at > CURRENT_TIMESTAMP)
		  AND recurrence >= ?`, minRecurrence).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("failed to count recurring dreams: %w", err)
	}
	return n, nil
}

// ResolveKnowledge records a human verdict on a triple. Confirm promotes it
// to grounded and verified — the only path to verified=true for
// dreamer-written triples. Reject expires it rather than deleting: even a
// dismissed dream stays in the log, demoted.
func (s *Store) ResolveKnowledge(ctx context.Context, id string, confirm bool) error {
	var query string
	if confirm {
		query = `UPDATE knowledge SET grounded = TRUE, verified = TRUE, expired_at = NULL WHERE id = ?`
	} else {
		query = `UPDATE knowledge SET expired_at = CURRENT_TIMESTAMP WHERE id = ?`
	}
	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to resolve knowledge: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("knowledge triple not found: %s", id)
	}
	return nil
}
