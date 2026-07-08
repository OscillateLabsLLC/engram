package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
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
		SELECT id, source_episode_id, target_episode_id, relationship, via_entity_id, weight, created_at
		FROM (
			SELECT *, MIN(hop) OVER (PARTITION BY id) AS min_hop,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY hop) AS rn
			FROM walk
		)
		WHERE rn = 1
		ORDER BY min_hop, created_at
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
	// RecurringDreams are quarantined triples the Dreamer keeps re-extracting
	// from distinct episodes — candidates for human confirmation
	RecurringDreams []models.KnowledgeTriple `json:"recurring_dreams"`
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
		RecurringDreams:  []models.KnowledgeTriple{},
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

	// Section 4: quarantined triples that keep recurring — ideas the Dreamer
	// keeps having that nothing has anchored, surfaced for human review
	dreams, err := s.ListRecurringDreams(ctx, groupID, DefaultDreamRecurrence, limit)
	if err != nil {
		return nil, err
	}
	res.RecurringDreams = dreams

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

// backfillHubCap mirrors the dreamer's maxSharedEpisodeLinks: an entity shared
// by more than this many episodes is a hub and its same_entity links are
// skipped, since linking everything through a ubiquitous entity carries no
// signal and grows quadratically.
const backfillHubCap = 25

// BackfillLinkStats reports the outcome of a same_entity link backfill.
type BackfillLinkStats struct {
	Entities        int            `json:"entities"`         // entities examined
	HubsSkipped     int            `json:"hubs_skipped"`     // entities over the hub cap
	LinksInserted   int            `json:"links_inserted"`   // new episode_links rows created
	PairsConsidered int            `json:"pairs_considered"` // directed (episode, other) pairs seen
	HubCap          int            `json:"hub_cap"`          // the cap applied this run
	HubLinksPruned  int            `json:"hub_links_pruned"` // stale over-cap same_entity links removed
	SharePercentile map[string]int `json:"share_percentile"` // eps/entity distribution (p50..p99,max)
}

// pruneHubLinks deletes same_entity episode links whose via_entity_id is shared
// by more than hubCap episodes — the hub entities the adaptive cap now excludes.
// This retracts co-occurrence noise a looser prior cap admitted. Typed edges
// (supersedes/contradicts) have a NULL via_entity_id and are never touched.
func (s *Store) pruneHubLinks(ctx context.Context, hubCap int) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM episode_links
		WHERE relationship = 'same_entity'
		  AND via_entity_id IN (
			SELECT entity_id FROM (
				SELECT entity_id, COUNT(DISTINCT source_episode_id) AS eps FROM (
					SELECT subject_entity_id AS entity_id, source_episode_id FROM knowledge
					WHERE subject_entity_id IS NOT NULL AND subject_entity_id != ''
					  AND source_episode_id IS NOT NULL AND source_episode_id != ''
					UNION
					SELECT object_entity_id AS entity_id, source_episode_id FROM knowledge
					WHERE object_entity_id IS NOT NULL AND object_entity_id != ''
					  AND source_episode_id IS NOT NULL AND source_episode_id != ''
				) GROUP BY entity_id
			) WHERE eps > ?
		  )
	`, hubCap)
	if err != nil {
		return 0, fmt.Errorf("failed to prune hub links: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return int(n), nil
}

// entityShareCounts returns, for every entity referenced by a triple, the
// number of distinct episodes referencing it — the quantity the hub cap is
// applied to. Counts only; no entity identity leaves this method.
func (s *Store) entityShareCounts(ctx context.Context) ([]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COUNT(DISTINCT source_episode_id) AS eps FROM (
			SELECT subject_entity_id AS entity_id, source_episode_id FROM knowledge
			WHERE subject_entity_id IS NOT NULL AND subject_entity_id != ''
			  AND source_episode_id IS NOT NULL AND source_episode_id != ''
			UNION
			SELECT object_entity_id AS entity_id, source_episode_id FROM knowledge
			WHERE object_entity_id IS NOT NULL AND object_entity_id != ''
			  AND source_episode_id IS NOT NULL AND source_episode_id != ''
		) GROUP BY entity_id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to compute entity share counts: %w", err)
	}
	defer rows.Close()
	var counts []int
	for rows.Next() {
		var eps int
		if err := rows.Scan(&eps); err != nil {
			return nil, err
		}
		counts = append(counts, eps)
	}
	return counts, rows.Err()
}

// adaptiveHubCap picks the episodes-per-entity threshold above which an entity
// is treated as a hub. A dense personal store connects almost everything
// through a handful of topic entities ("Homelab", "Caldera"); a fixed cap of 25
// let those through and drowned real links in co-occurrence noise. The adaptive
// cap is the 90th percentile of the eps/entity distribution, floored at
// hubCapFloor so a sparse store doesn't over-prune. This means "an entity in
// more episodes than 90% of entities is a topic, not a connection."
const hubCapFloor = 6

func adaptiveHubCap(counts []int) int {
	if len(counts) == 0 {
		return hubCapFloor
	}
	sorted := make([]int, len(counts))
	copy(sorted, counts)
	sortInts(sorted)
	idx := (len(sorted) * 90) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	cap := sorted[idx]
	if cap < hubCapFloor {
		cap = hubCapFloor
	}
	return cap
}

func percentileMap(counts []int) map[string]int {
	if len(counts) == 0 {
		return map[string]int{}
	}
	sorted := make([]int, len(counts))
	copy(sorted, counts)
	sortInts(sorted)
	at := func(p int) int {
		i := (len(sorted) * p) / 100
		if i >= len(sorted) {
			i = len(sorted) - 1
		}
		return sorted[i]
	}
	return map[string]int{
		"p50": at(50), "p75": at(75), "p90": at(90),
		"p95": at(95), "p99": at(99), "max": sorted[len(sorted)-1],
	}
}

func sortInts(a []int) { sort.Ints(a) }

// BackfillEpisodeLinks derives same_entity episode links from the existing
// knowledge graph, without re-running enrichment. For every entity referenced
// by a triple it links each source episode to every other episode sharing that
// entity — the exact derivation the dreamer performs incrementally in
// linkSharedEntityEpisodes, run in one batch over the whole corpus. It is
// idempotent: InsertEpisodeLink's INSERT OR IGNORE dedups on
// (source, target, relationship), so re-running only adds links for newly
// shared entities. Hub entities over backfillHubCap are skipped, matching the
// dreamer. Only episode_links is written; knowledge and entities are untouched.
func (s *Store) BackfillEpisodeLinks(ctx context.Context) (BackfillLinkStats, error) {
	var stats BackfillLinkStats

	// Derive the hub cap from this corpus's own eps/entity distribution so a
	// dense personal store prunes its topic entities instead of drowning real
	// links in co-occurrence noise. Report the distribution for transparency.
	shareCounts, err := s.entityShareCounts(ctx)
	if err != nil {
		return stats, err
	}
	hubCap := adaptiveHubCap(shareCounts)
	stats.HubCap = hubCap
	stats.SharePercentile = percentileMap(shareCounts)

	// Prune same_entity links routed through entities now over the cap. A prior
	// run at a looser cap may have inserted hub links this run would skip;
	// lowering the cap only matters if those stale hub links are removed too.
	// Only same_entity links carry via_entity_id, so typed edges are untouched.
	pruned, err := s.pruneHubLinks(ctx, hubCap)
	if err != nil {
		return stats, err
	}
	stats.HubLinksPruned = pruned

	// Every entity that participates in at least one triple, as subject or object.
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT entity_id FROM (
			SELECT subject_entity_id AS entity_id FROM knowledge
			WHERE subject_entity_id IS NOT NULL AND subject_entity_id != ''
			UNION
			SELECT object_entity_id AS entity_id FROM knowledge
			WHERE object_entity_id IS NOT NULL AND object_entity_id != ''
		)
	`)
	if err != nil {
		return stats, fmt.Errorf("failed to list entities for backfill: %w", err)
	}
	var entityIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return stats, fmt.Errorf("failed to scan entity id: %w", err)
		}
		entityIDs = append(entityIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return stats, err
	}
	rows.Close()

	// For each entity, find every episode referencing it and link each such
	// episode to the others through this entity. FindEpisodesSharingEntities
	// excludes one episode; we call it once per episode in the shared set so
	// links are created in both directions (the UNIQUE constraint keeps the
	// mirror pair from double-counting on read).
	for _, entityID := range entityIDs {
		stats.Entities++

		// All episodes referencing this entity: pass empty exclude to get the
		// full set, then fan out per-episode.
		episodes, err := s.FindEpisodesSharingEntities(ctx, []string{entityID}, "")
		if err != nil {
			return stats, fmt.Errorf("backfill: find episodes for entity %s: %w", entityID, err)
		}
		if len(episodes) < 2 {
			continue // nothing to link
		}
		if len(episodes) > hubCap {
			stats.HubsSkipped++
			continue
		}

		for i, src := range episodes {
			for j, dst := range episodes {
				if i == j {
					continue
				}
				stats.PairsConsidered++
				link := &models.EpisodeLink{
					SourceEpisodeID: src,
					TargetEpisodeID: dst,
					Relationship:    "same_entity",
					ViaEntityID:     entityID,
				}
				before := stats.LinksInserted
				inserted, err := s.insertEpisodeLinkCounted(ctx, link)
				if err != nil {
					return stats, fmt.Errorf("backfill: link %s->%s via %s: %w", src, dst, entityID, err)
				}
				if inserted {
					stats.LinksInserted = before + 1
				}
			}
		}
	}

	return stats, nil
}

// insertEpisodeLinkCounted inserts a link and reports whether a new row was
// created (false when INSERT OR IGNORE skipped a duplicate). It mirrors
// InsertEpisodeLink but returns the affected-row count so the backfill can
// distinguish new links from re-runs.
func (s *Store) insertEpisodeLinkCounted(ctx context.Context, link *models.EpisodeLink) (bool, error) {
	id := link.ID
	if id == "" {
		id = link.SourceEpisodeID + ":" + link.TargetEpisodeID + ":" + link.Relationship + ":" + link.ViaEntityID
	}
	weight := link.Weight
	if weight == 0 {
		weight = 1.0
	}
	var viaEntityID interface{}
	if link.ViaEntityID != "" {
		viaEntityID = link.ViaEntityID
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO episode_links (id, source_episode_id, target_episode_id, relationship, via_entity_id, weight, created_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, id, link.SourceEpisodeID, link.TargetEpisodeID, link.Relationship, viaEntityID, weight)
	if err != nil {
		return false, fmt.Errorf("failed to insert episode link: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, nil // driver can't report; treat as inserted-unknown, non-fatal
	}
	return n > 0, nil
}

// deleteEpisodeLink removes a specific (source, target, relationship) edge if it
// exists. Used to retract a stale reverse supersedes edge when orientation flips
// the direction, so the graph never carries a bidirectional supersession.
func (s *Store) deleteEpisodeLink(ctx context.Context, source, target, relationship string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM episode_links
		WHERE source_episode_id = ? AND target_episode_id = ? AND relationship = ?
	`, source, target, relationship)
	if err != nil {
		return fmt.Errorf("failed to delete episode link: %w", err)
	}
	return nil
}
