package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/oscillatelabsllc/engram/internal/models"
)

// traverseMaxDepth bounds how many hops TraverseEpisodeLinks will walk
const traverseMaxDepth = 3

// traverseMaxLinks caps the total number of links a traversal returns
const traverseMaxLinks = 50

// TraverseEpisodeLinks walks the episode_links graph from startEpisodeID in
// both directions, up to depth hops (clamped to 3), returning the distinct
// links encountered. Total fan-out is capped at 50 links.
func (s *Store) TraverseEpisodeLinks(ctx context.Context, startEpisodeID string, depth int) ([]models.EpisodeLink, error) {
	if depth <= 0 {
		return nil, nil
	}
	if depth > traverseMaxDepth {
		depth = traverseMaxDepth
	}

	query := fmt.Sprintf(`
		WITH RECURSIVE walk AS (
			SELECT l.id, l.source_episode_id, l.target_episode_id, l.relationship,
			       l.via_entity_id, l.weight, l.created_at,
			       CASE WHEN l.source_episode_id = ? THEN l.target_episode_id ELSE l.source_episode_id END AS frontier,
			       1 AS hop
			FROM episode_links l
			WHERE l.source_episode_id = ? OR l.target_episode_id = ?
			UNION
			SELECT l.id, l.source_episode_id, l.target_episode_id, l.relationship,
			       l.via_entity_id, l.weight, l.created_at,
			       CASE WHEN l.source_episode_id = w.frontier THEN l.target_episode_id ELSE l.source_episode_id END AS frontier,
			       w.hop + 1
			FROM episode_links l
			JOIN walk w ON (l.source_episode_id = w.frontier OR l.target_episode_id = w.frontier)
			WHERE w.hop < ?
		)
		SELECT DISTINCT id, source_episode_id, target_episode_id, relationship, via_entity_id, weight, created_at
		FROM walk
		LIMIT %d
	`, traverseMaxLinks)

	rows, err := s.db.QueryContext(ctx, query, startEpisodeID, startEpisodeID, startEpisodeID, depth)
	if err != nil {
		return nil, fmt.Errorf("failed to traverse episode links: %w", err)
	}
	defer rows.Close()

	var links []models.EpisodeLink
	for rows.Next() {
		var link models.EpisodeLink
		var viaEntityID sql.NullString
		if err := rows.Scan(&link.ID, &link.SourceEpisodeID, &link.TargetEpisodeID,
			&link.Relationship, &viaEntityID, &link.Weight, &link.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan traversed link: %w", err)
		}
		if viaEntityID.Valid {
			link.ViaEntityID = viaEntityID.String
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

// LooseEnds reports weakly-connected corners of the memory graph that could
// benefit from enrichment or manual linking.
type LooseEnds struct {
	// UnlinkedEpisodes have no episode links and no derived knowledge
	UnlinkedEpisodes []models.Episode `json:"unlinked_episodes"`
	// DanglingEntities appear in exactly one knowledge triple
	DanglingEntities []models.Entity `json:"dangling_entities"`
	// IsolatedClusters are connected components of the episode link graph
	// with 3 or fewer episodes
	IsolatedClusters [][]string `json:"isolated_clusters"`
}

// FindLooseEnds surfaces unlinked episodes, degree-1 entities, and small
// isolated clusters in the episode link graph. groupID optionally scopes the
// results; limit bounds each section independently.
func (s *Store) FindLooseEnds(ctx context.Context, groupID string, limit int) (*LooseEnds, error) {
	if limit <= 0 {
		limit = 10
	}
	res := &LooseEnds{
		UnlinkedEpisodes: []models.Episode{},
		DanglingEntities: []models.Entity{},
		IsolatedClusters: [][]string{},
	}

	// Section 1: episodes with no links in either direction and no knowledge
	// derived from them
	unlinkedQuery := fmt.Sprintf(`
		SELECT %s, NULL AS similarity, NULL AS relevance
		FROM episodes e
		WHERE NOT EXISTS (
			SELECT 1 FROM episode_links l
			WHERE l.source_episode_id = e.id OR l.target_episode_id = e.id
		)
		AND NOT EXISTS (
			SELECT 1 FROM knowledge k WHERE k.source_episode_id = e.id
		)`, episodeCols)
	var args []interface{}
	if groupID != "" {
		unlinkedQuery += " AND e.group_id = ?"
		args = append(args, groupID)
	}
	unlinkedQuery += " ORDER BY e.created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, unlinkedQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to find unlinked episodes: %w", err)
	}
	episodes, err := s.scanEpisodes(rows)
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to scan unlinked episodes: %w", err)
	}
	if episodes != nil {
		res.UnlinkedEpisodes = episodes
	}

	// Section 2: entities referenced by exactly one knowledge triple
	danglingQuery := `
		SELECT e.id, e.canonical_name, e.entity_type, e.group_id, e.created_at
		FROM entities e
		JOIN (
			SELECT entity_id FROM (
				SELECT subject_entity_id AS entity_id FROM knowledge
				UNION ALL
				SELECT object_entity_id AS entity_id FROM knowledge
			) refs
			GROUP BY entity_id
			HAVING COUNT(*) = 1
		) d ON e.id = d.entity_id`
	args = nil
	if groupID != "" {
		danglingQuery += " WHERE e.group_id = ?"
		args = append(args, groupID)
	}
	danglingQuery += " ORDER BY e.created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err = s.db.QueryContext(ctx, danglingQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to find dangling entities: %w", err)
	}
	for rows.Next() {
		var e models.Entity
		if err := rows.Scan(&e.ID, &e.CanonicalName, &e.EntityType, &e.GroupID, &e.CreatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to scan dangling entity: %w", err)
		}
		res.DanglingEntities = append(res.DanglingEntities, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// Section 3: connected components of the episode link graph with <= 3
	// members. Transitive closure via recursive CTE; component identity is the
	// minimum reachable episode id. No bound parameters here — DuckDB fails to
	// bind parameters inside this multi-referenced recursive CTE, so the group
	// filter is applied afterwards with a separate parameterized query.
	clusterQuery := `
		WITH RECURSIVE
		edges AS (
			SELECT l.source_episode_id AS a, l.target_episode_id AS b FROM episode_links l
			UNION
			SELECT l.target_episode_id AS a, l.source_episode_id AS b FROM episode_links l
		),
		reach AS (
			SELECT a AS node, b AS other FROM edges
			UNION
			SELECT r.node, e.b FROM reach r JOIN edges e ON r.other = e.a
		),
		comp AS (
			SELECT node, MIN(other) AS root FROM (
				SELECT node, other FROM reach
				UNION
				SELECT a AS node, a AS other FROM edges
			) all_pairs
			GROUP BY node
		)
		SELECT list(node) AS members
		FROM comp
		GROUP BY root
		HAVING COUNT(*) <= 3
	`

	rows, err = s.db.QueryContext(ctx, clusterQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to find isolated clusters: %w", err)
	}
	var clusters [][]string
	for rows.Next() {
		var membersRaw interface{}
		if err := rows.Scan(&membersRaw); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to scan cluster members: %w", err)
		}
		var members []string
		switch v := membersRaw.(type) {
		case []interface{}:
			for _, m := range v {
				if s, ok := m.(string); ok {
					members = append(members, s)
				}
			}
		case []string:
			members = v
		}
		if len(members) > 0 {
			clusters = append(clusters, members)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	inGroup, err := s.episodeGroupFilter(ctx, groupID, clusters)
	if err != nil {
		return nil, err
	}
	for _, members := range clusters {
		if len(res.IsolatedClusters) >= limit {
			break
		}
		if inGroup == nil || anyMember(members, inGroup) {
			res.IsolatedClusters = append(res.IsolatedClusters, members)
		}
	}
	return res, nil
}

// episodeGroupFilter returns the set of cluster-member episode IDs that belong
// to groupID, or nil when no group filter is requested.
func (s *Store) episodeGroupFilter(ctx context.Context, groupID string, clusters [][]string) (map[string]bool, error) {
	if groupID == "" {
		return nil, nil
	}
	var ids []string
	for _, members := range clusters {
		ids = append(ids, members...)
	}
	if len(ids) == 0 {
		return map[string]bool{}, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]interface{}, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, groupID)

	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf("SELECT id FROM episodes WHERE id IN (%s) AND group_id = ?", placeholders), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to filter clusters by group: %w", err)
	}
	defer rows.Close()

	inGroup := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan cluster group filter: %w", err)
		}
		inGroup[id] = true
	}
	return inGroup, rows.Err()
}

// anyMember reports whether any member id is present in the set
func anyMember(members []string, set map[string]bool) bool {
	for _, m := range members {
		if set[m] {
			return true
		}
	}
	return false
}
