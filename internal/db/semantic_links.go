package db

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/oscillatelabsllc/engram/internal/models"
)

// Semantic episode-link derivation.
//
// The dreamer's only automatic edge producer is linkSharedEntityEpisodes, which
// emits same_entity co-occurrence links. The typed relationships that make the
// graph a *reasoning* graph rather than a co-occurrence expander —
// supersedes/contradicts — had no automatic producer at all; they were only
// writable by hand via the link_episodes MCP tool, so graph_depth traversal
// could never answer "what superseded what" even though episodes state the
// relationship in their own prose ("SUPERSEDES <id>", "CORRECTION", "DEAD END").
//
// This pass reads that prose. When an episode explicitly names a marker keyword
// near a resolvable episode ID, it promotes the statement into a typed edge:
// the episode containing the marker is the source; the named episode is the
// target it supersedes or contradicts. Only explicit, ID-bearing statements are
// promoted — fuzzy "correction of the earlier plan" cases without an ID are
// left for a future LLM-assisted pass, since guessing the target would inject
// exactly the noise typed edges are supposed to remove.

// supersedesMarker matches keywords asserting that the current episode replaces
// a prior one. contradictsMarker matches keywords asserting a prior episode was
// wrong / abandoned. Both are matched case-insensitively; the corpus writes
// them in caps ("SUPERSEDES", "DEAD END") but lowercase prose is caught too.
var (
	supersedesMarker = regexp.MustCompile(`(?i)\b(supersede[sd]?|superseding|replace[sd]?|replacing|retract(?:s|ed)?|obsolete[sd]?|deprecate[sd]?)\b`)
	contradictsMarker = regexp.MustCompile(`(?i)\b(correction|dead[\s-]?end|retracted|was wrong|no longer (?:valid|correct|true|the case)|abandoned)\b`)

	// episodeIDRef matches either a full UUID or a bare 8+ hex-char prefix. The
	// corpus references superseded episodes by either form. A prefix is only
	// accepted if it resolves to exactly one real episode (see resolveIDRef).
	episodeIDRef = regexp.MustCompile(`\b([0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}|[0-9a-fA-F]{8,})\b`)
)

// markerWindow bounds how far after a marker keyword an ID may appear and still
// be attributed to that marker. "SUPERSEDES 658ba3a2, 7e8fe9be, c05a5da2"
// keeps all three within a short window; an unrelated UUID paragraphs later is
// not attached.
const markerWindow = 200

// SemanticLinkStats reports the outcome of a semantic-link backfill.
type SemanticLinkStats struct {
	EpisodesScanned    int `json:"episodes_scanned"`
	EpisodesWithMarker int `json:"episodes_with_marker"`
	SupersedesInserted int `json:"supersedes_inserted"`
	ContradictsInserted int `json:"contradicts_inserted"`
	RefsUnresolved     int `json:"refs_unresolved"` // marker IDs that matched 0 or >1 episodes
	SelfRefsSkipped    int `json:"self_refs_skipped"`
}

// BackfillSemanticEpisodeLinks scans every episode's prose for explicit
// supersession/correction markers that name a resolvable target episode ID, and
// promotes each into a typed supersedes/contradicts edge. It writes only
// episode_links; knowledge and entities are untouched. It is idempotent
// (InsertEpisodeLink dedups on (source,target,relationship)). No LLM is used —
// this is deterministic prose matching.
func (s *Store) BackfillSemanticEpisodeLinks(ctx context.Context) (SemanticLinkStats, error) {
	var stats SemanticLinkStats

	// Load IDs (for resolution) and created-at (for supersedes orientation).
	idx, err := s.loadEpisodeIndex(ctx)
	if err != nil {
		return stats, err
	}
	idSet, prefixIndex := idx.idSet, idx.prefixIndex

	rows, err := s.db.QueryContext(ctx, `SELECT id, content, name FROM episodes`)
	if err != nil {
		return stats, fmt.Errorf("failed to scan episodes for semantic links: %w", err)
	}
	defer rows.Close()

	type work struct {
		sourceID string
		text     string
	}
	var todo []work
	for rows.Next() {
		var id, content, name string
		if err := rows.Scan(&id, &content, &name); err != nil {
			return stats, fmt.Errorf("failed to scan episode row: %w", err)
		}
		text := content
		if name != "" {
			text = name + "\n\n" + content
		}
		todo = append(todo, work{sourceID: id, text: text})
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}

	for _, w := range todo {
		stats.EpisodesScanned++
		sup := findRefsAfterMarker(w.text, supersedesMarker)
		con := findRefsAfterMarker(w.text, contradictsMarker)
		if len(sup) == 0 && len(con) == 0 {
			continue
		}
		stats.EpisodesWithMarker++

		emit := func(refs []string, rel string, counter *int) error {
			for _, ref := range refs {
				targetID, ok := resolveIDRef(ref, idSet, prefixIndex)
				if !ok {
					stats.RefsUnresolved++
					continue
				}
				if targetID == w.sourceID {
					stats.SelfRefsSkipped++
					continue
				}

				src, dst := w.sourceID, targetID
				if rel == "supersedes" {
					// Orient newer→older and remove any stale reverse edge so the
					// graph can never carry A-supersedes-B AND B-supersedes-A.
					src, dst = orientSupersedes(w.sourceID, targetID, idx.createdAt)
					if err := s.deleteEpisodeLink(ctx, dst, src, "supersedes"); err != nil {
						return err
					}
				}

				inserted, err := s.insertEpisodeLinkCounted(ctx, &models.EpisodeLink{
					SourceEpisodeID: src,
					TargetEpisodeID: dst,
					Relationship:    rel,
				})
				if err != nil {
					return err
				}
				if inserted {
					*counter++
				}
			}
			return nil
		}

		if err := emit(sup, "supersedes", &stats.SupersedesInserted); err != nil {
			return stats, fmt.Errorf("emit supersedes from %s: %w", w.sourceID, err)
		}
		if err := emit(con, "contradicts", &stats.ContradictsInserted); err != nil {
			return stats, fmt.Errorf("emit contradicts from %s: %w", w.sourceID, err)
		}
	}

	return stats, nil
}

// MarkerBreakdown categorizes episodes carrying a supersession/correction
// marker by whether the marker names a resolvable target. It is the number that
// decides whether a fuzzy (LLM-assisted) target resolver is worth building: the
// deterministic producer can only reach the "with resolvable id" bucket.
type MarkerBreakdown struct {
	EpisodesWithMarker int `json:"episodes_with_marker"`
	WithResolvableID   int `json:"with_resolvable_id"`   // ≥1 ref resolves → produces edges
	WithDanglingID     int `json:"with_dangling_id"`     // has an id-shaped ref, none resolve
	WithNoID           int `json:"with_no_id"`           // marker present, no id-shaped token at all
}

// MarkerReferenceBreakdown scans every episode for markers and buckets them by
// reference resolvability. Counts only.
func (s *Store) MarkerReferenceBreakdown(ctx context.Context) (MarkerBreakdown, error) {
	var b MarkerBreakdown
	idx, err := s.loadEpisodeIndex(ctx)
	if err != nil {
		return b, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, content, name FROM episodes`)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, content, name string
		if err := rows.Scan(&id, &content, &name); err != nil {
			return b, err
		}
		text := content
		if name != "" {
			text = name + "\n\n" + content
		}
		refs := append(findRefsAfterMarker(text, supersedesMarker),
			findRefsAfterMarker(text, contradictsMarker)...)
		// Was there a marker at all? (findRefs only returns refs; check markers
		// independently so an id-less marker still counts.)
		hasMarker := supersedesMarker.MatchString(text) || contradictsMarker.MatchString(text)
		if !hasMarker {
			continue
		}
		b.EpisodesWithMarker++
		if len(refs) == 0 {
			b.WithNoID++
			continue
		}
		anyResolved := false
		for _, r := range refs {
			if _, ok := resolveIDRef(r, idx.idSet, idx.prefixIndex); ok {
				anyResolved = true
				break
			}
		}
		if anyResolved {
			b.WithResolvableID++
		} else {
			b.WithDanglingID++
		}
	}
	return b, rows.Err()
}

// LinkSemanticEpisodeMarkers scans a single episode's text for explicit
// supersession/correction markers naming resolvable target IDs and emits the
// corresponding typed edges. It is the per-episode form of
// BackfillSemanticEpisodeLinks, called by the dreamer during enrichment so new
// episodes get typed edges without a full-corpus rescan. Returns the number of
// supersedes and contradicts edges inserted.
func (s *Store) LinkSemanticEpisodeMarkers(ctx context.Context, ep *models.Episode) (supersedes, contradicts int, err error) {
	text := ep.Content
	if ep.Name != "" {
		text = ep.Name + "\n\n" + ep.Content
	}
	sup := findRefsAfterMarker(text, supersedesMarker)
	con := findRefsAfterMarker(text, contradictsMarker)
	if len(sup) == 0 && len(con) == 0 {
		return 0, 0, nil
	}

	idx, err := s.loadEpisodeIndex(ctx)
	if err != nil {
		return 0, 0, err
	}
	idSet, prefixIndex := idx.idSet, idx.prefixIndex

	emit := func(refs []string, rel string) (int, error) {
		n := 0
		for _, ref := range refs {
			targetID, ok := resolveIDRef(ref, idSet, prefixIndex)
			if !ok || targetID == ep.ID {
				continue
			}
			src, dst := ep.ID, targetID
			if rel == "supersedes" {
				src, dst = orientSupersedes(ep.ID, targetID, idx.createdAt)
				if err := s.deleteEpisodeLink(ctx, dst, src, "supersedes"); err != nil {
					return n, err
				}
			}
			inserted, err := s.insertEpisodeLinkCounted(ctx, &models.EpisodeLink{
				SourceEpisodeID: src,
				TargetEpisodeID: dst,
				Relationship:    rel,
			})
			if err != nil {
				return n, err
			}
			if inserted {
				n++
			}
		}
		return n, nil
	}

	if supersedes, err = emit(sup, "supersedes"); err != nil {
		return supersedes, 0, err
	}
	if contradicts, err = emit(con, "contradicts"); err != nil {
		return supersedes, contradicts, err
	}
	return supersedes, contradicts, nil
}

// findRefsAfterMarker returns every episode-ID-shaped token that appears within
// markerWindow characters after any marker keyword match. Multiple IDs after a
// single marker ("SUPERSEDES a, b, c") are all captured. Deduplicated per
// episode.
func findRefsAfterMarker(text string, marker *regexp.Regexp) []string {
	locs := marker.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var refs []string
	for _, loc := range locs {
		end := loc[1]
		windowEnd := end + markerWindow
		if windowEnd > len(text) {
			windowEnd = len(text)
		}
		window := text[end:windowEnd]
		for _, m := range episodeIDRef.FindAllStringSubmatch(window, -1) {
			ref := strings.ToLower(m[1])
			if !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// resolveIDRef resolves a full or prefix ID reference to a real episode ID.
// A full UUID must match an existing episode exactly. A prefix (< full-UUID
// length) is accepted only if exactly one episode ID starts with it — an
// ambiguous or absent prefix resolves to (,"" false) and is counted unresolved
// rather than guessed.
func resolveIDRef(ref string, idSet map[string]bool, prefixIndex map[string][]string) (string, bool) {
	ref = strings.ToLower(ref)
	if idSet[ref] {
		return ref, true
	}
	// Prefix resolution: index is keyed by the 8-char prefix.
	if len(ref) >= 8 {
		key := ref[:8]
		candidates := prefixIndex[key]
		var matches []string
		for _, cand := range candidates {
			if strings.HasPrefix(cand, ref) {
				matches = append(matches, cand)
			}
		}
		if len(matches) == 1 {
			return matches[0], true
		}
	}
	return "", false
}

// episodeIDIndex holds everything the semantic pass needs to resolve and orient
// references: the ID set and 8-char prefix index for resolution, and a
// created-at map (keyed by lowercased ID) for recency orientation.
type episodeIDIndex struct {
	idSet       map[string]bool
	prefixIndex map[string][]string
	createdAt   map[string]time.Time
}

// loadEpisodeIDIndex returns the ID set, prefix index, and created-at map for
// all episodes. IDs are lowercased so resolution and orientation agree.
func (s *Store) loadEpisodeIDIndex(ctx context.Context) (map[string]bool, map[string][]string, error) {
	idx, err := s.loadEpisodeIndex(ctx)
	if err != nil {
		return nil, nil, err
	}
	return idx.idSet, idx.prefixIndex, nil
}

func (s *Store) loadEpisodeIndex(ctx context.Context) (*episodeIDIndex, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, created_at FROM episodes`)
	if err != nil {
		return nil, fmt.Errorf("failed to load episode index: %w", err)
	}
	defer rows.Close()

	idx := &episodeIDIndex{
		idSet:       map[string]bool{},
		prefixIndex: map[string][]string{},
		createdAt:   map[string]time.Time{},
	}
	for rows.Next() {
		var id string
		var createdAt time.Time
		if err := rows.Scan(&id, &createdAt); err != nil {
			return nil, err
		}
		lid := strings.ToLower(id)
		idx.idSet[lid] = true
		idx.createdAt[lid] = createdAt
		if len(lid) >= 8 {
			key := lid[:8]
			idx.prefixIndex[key] = append(idx.prefixIndex[key], lid)
		}
	}
	return idx, rows.Err()
}

// orientSupersedes enforces the newer→older invariant for a supersedes edge.
// Given a marker in `sourceID`'s prose naming `targetID`, it returns the
// (newer, older) pair by created-at, so the edge always points from the current
// episode to the one it replaced — regardless of whether the prose said
// "supersedes <old>" (in the new episode) or "superseded by <new>" (annotated
// on the old one). When timestamps are missing or equal it falls back to the
// prose direction (source→target). contradicts edges are NOT reoriented: a
// correction legitimately points from the corrector to the corrected, and that
// is already source→target.
func orientSupersedes(sourceID, targetID string, createdAt map[string]time.Time) (newer, older string) {
	st, sok := createdAt[strings.ToLower(sourceID)]
	tt, tok := createdAt[strings.ToLower(targetID)]
	if sok && tok && tt.After(st) {
		// target is newer → it supersedes source; flip.
		return targetID, sourceID
	}
	return sourceID, targetID
}
