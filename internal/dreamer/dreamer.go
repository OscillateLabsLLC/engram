// Package dreamer implements Engram's asynchronous knowledge-extraction
// worker. It crawls episodes that have not been enriched yet, asks an LLM to
// extract subject-predicate-object triples, validates them deterministically,
// and writes entities, knowledge triples, and episode links. All output is
// derived data: the dreamer never blocks or mutates episode content.
package dreamer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/models"
)

// LLM produces structured JSON completions. Both adapters in internal/llm
// satisfy this interface.
type LLM interface {
	ChatJSON(ctx context.Context, system, user, schema string) (json.RawMessage, error)
	// Model returns the model name, used to stamp triple provenance
	Model() string
}

// Embedder generates vector embeddings for text
type Embedder interface {
	Generate(ctx context.Context, text string) ([]float32, error)
	// Model returns the embedding model name, used to stamp provenance
	Model() string
}

const (
	// batchSize is how many unenriched episodes are fetched per crawl page
	batchSize = 50

	// maxConsecutiveLLMFailures aborts a crawl when the LLM endpoint appears
	// down or rate-limited — every remaining episode would burn a failed call
	maxConsecutiveLLMFailures = 5
	// maxTriplesPerEpisode caps how many validated triples are stored per episode
	maxTriplesPerEpisode = 10
	// entityResolutionThreshold is passed to InsertEntity for entity dedup
	entityResolutionThreshold = 0.88
	// DefaultLLMTimeout bounds a single extraction call
	DefaultLLMTimeout = 60 * time.Second
	// embedTimeout bounds a single embedding call
	embedTimeout = 5 * time.Second
)

// extractionSystemPrompt instructs the model; the deterministic validation
// pipeline below enforces every rule it states.
var extractionSystemPrompt = "You extract knowledge triples from text. Output only JSON matching the schema. Rules: subject and object MUST be entity names that appear in or are directly implied by the text. predicate MUST be one of: " + strings.Join(sortedPredicates(), ", ") + ". confidence is 0.0-1.0 reflecting how explicitly the text states the fact. Extract at most 10 triples. If no clear facts exist, return an empty list."

// sortedPredicates renders models.ValidPredicates deterministically so the
// prompt can never drift from the validation whitelist
func sortedPredicates() []string {
	preds := make([]string, 0, len(models.ValidPredicates))
	for p := range models.ValidPredicates {
		preds = append(preds, p)
	}
	sort.Strings(preds)
	return preds
}

// extractionSchema is the JSON schema for the extraction envelope
const extractionSchema = `{"type":"object","properties":{"triples":{"type":"array","items":{"type":"object","properties":{"subject":{"type":"string"},"predicate":{"type":"string"},"object":{"type":"string"},"subject_type":{"type":"string"},"object_type":{"type":"string"},"confidence":{"type":"number"}},"required":["subject","predicate","object","confidence"]}}},"required":["triples"]}`

// probeSchema is a trivial schema used to check LLM reachability
const probeSchema = `{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`

// Dreamer crawls unenriched episodes and extracts knowledge from them
type Dreamer struct {
	store      *db.Store
	llm        LLM
	embedder   Embedder
	llmTimeout time.Duration
}

// New creates a Dreamer. llmTimeout bounds each extraction call; <= 0 uses
// DefaultLLMTimeout.
func New(store *db.Store, llm LLM, embedder Embedder, llmTimeout time.Duration) *Dreamer {
	if llmTimeout <= 0 {
		llmTimeout = DefaultLLMTimeout
	}
	return &Dreamer{
		store:      store,
		llm:        llm,
		embedder:   embedder,
		llmTimeout: llmTimeout,
	}
}

// Probe checks that the LLM is reachable with a trivial structured call
func (d *Dreamer) Probe(ctx context.Context) error {
	_, err := d.llm.ChatJSON(ctx, "You are a health check. Output only JSON.", `Return {"ok": true}`, probeSchema)
	return err
}

// ErrLLMFailure marks failures of the LLM call itself (endpoint down, rate
// limit, timeout). Episodes hitting it are left unenriched so a later run
// retries them, unlike content-shaped failures which are stamped.
var ErrLLMFailure = errors.New("llm call failed")

// Process crawls unenriched episodes in batches until none remain or the
// context is cancelled. Content-shaped failures (bad extraction payload) are
// logged and stamped so poison episodes never loop; LLM-call failures leave
// the episode unenriched for a later run, and a streak of them aborts the
// crawl entirely — a dead endpoint would fail every remaining episode.
// onEpisode, when non-nil, is invoked after each episode with whether it
// failed — used by the admin job for progress reporting.
func (d *Dreamer) Process(ctx context.Context, onEpisode func(failed bool)) error {
	attempted := map[string]bool{}
	consecutiveLLMFailures := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		episodes, err := d.store.ListUnenrichedEpisodes(ctx, batchSize)
		if err != nil {
			return fmt.Errorf("listing unenriched episodes: %w", err)
		}

		progressed := false
		for i := range episodes {
			ep := &episodes[i]
			if attempted[ep.ID] {
				// Episode failed AND its enrichment stamp failed — skip it so
				// the crawl cannot loop forever
				continue
			}
			attempted[ep.ID] = true
			progressed = true

			if err := ctx.Err(); err != nil {
				return err
			}
			err := d.ProcessEpisode(ctx, ep)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: dreamer failed to enrich episode %s: %v\n", ep.ID, err)
			}
			if onEpisode != nil {
				onEpisode(err != nil)
			}

			if errors.Is(err, ErrLLMFailure) {
				consecutiveLLMFailures++
				if consecutiveLLMFailures >= maxConsecutiveLLMFailures {
					return fmt.Errorf("aborting crawl after %d consecutive LLM failures (endpoint down or rate-limited; unprocessed episodes remain queued): %w",
						consecutiveLLMFailures, err)
				}
			} else {
				consecutiveLLMFailures = 0
			}
		}

		if len(episodes) == 0 || !progressed {
			return nil
		}
	}
}

// ProcessEpisode extracts triples from a single episode and writes entities,
// knowledge, and episode links. A parse failure (bad payload from a working
// endpoint) stamps the episode enriched with the error recorded in metadata
// so poison episodes are never retried; a failure of the LLM call itself
// returns ErrLLMFailure and leaves the episode unenriched for a later run.
func (d *Dreamer) ProcessEpisode(ctx context.Context, ep *models.Episode) error {
	llmCtx, cancel := context.WithTimeout(ctx, d.llmTimeout)
	raw, err := d.llm.ChatJSON(llmCtx, extractionSystemPrompt, buildUserMessage(ep), extractionSchema)
	cancel()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrLLMFailure, err)
	}

	triples, err := validateExtraction(raw, ep.Name+"\n"+ep.Content)
	if err != nil {
		return d.failEnrichment(ctx, ep.ID, fmt.Errorf("extraction payload invalid: %w", err))
	}

	entityIDs := make(map[string]bool)
	for _, tr := range triples {
		subject, err := d.resolveEntity(ctx, tr.Subject, tr.SubjectType, ep.GroupID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: dreamer failed to resolve subject %q: %v\n", tr.Subject, err)
			continue
		}
		object, err := d.resolveEntity(ctx, tr.Object, tr.ObjectType, ep.GroupID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: dreamer failed to resolve object %q: %v\n", tr.Object, err)
			continue
		}

		tripleText := fmt.Sprintf("%s %s %s", subject.CanonicalName, tr.Predicate, object.CanonicalName)
		tripleEmb := d.embed(ctx, tripleText)

		triple := &models.KnowledgeTriple{
			SubjectEntityID: subject.ID,
			Predicate:       tr.Predicate,
			ObjectEntityID:  object.ID,
			SourceEpisodeID: ep.ID,
			Source:          "dreamer/" + d.llm.Model(),
			GroupID:         ep.GroupID,
			Embedding:       tripleEmb,
			EmbeddingModel:  d.embedder.Model(),
			Confidence:      tr.Confidence,
			Verified:        false,
		}
		if err := d.store.InsertKnowledgeTriple(ctx, triple); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: dreamer failed to store triple %q: %v\n", tripleText, err)
			continue
		}
		entityIDs[subject.ID] = true
		entityIDs[object.ID] = true
	}

	d.linkSharedEntityEpisodes(ctx, ep.ID, entityIDs)

	return d.store.MarkEpisodeEnriched(ctx, ep.ID, "")
}

// linkSharedEntityEpisodes creates same_entity links from episodeID to every
// other episode whose knowledge references one of the given entities
func (d *Dreamer) linkSharedEntityEpisodes(ctx context.Context, episodeID string, entityIDs map[string]bool) {
	for entityID := range entityIDs {
		shared, err := d.store.FindEpisodesSharingEntities(ctx, []string{entityID}, episodeID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: dreamer failed to find episodes sharing entity %s: %v\n", entityID, err)
			continue
		}
		for _, other := range shared {
			link := &models.EpisodeLink{
				SourceEpisodeID: episodeID,
				TargetEpisodeID: other,
				Relationship:    "same_entity",
				ViaEntityID:     entityID,
			}
			if err := d.store.InsertEpisodeLink(ctx, link); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: dreamer failed to link episodes %s -> %s: %v\n", episodeID, other, err)
			}
		}
	}
}

// failEnrichment stamps the episode enriched with the error recorded in its
// metadata and returns the original cause
func (d *Dreamer) failEnrichment(ctx context.Context, episodeID string, cause error) error {
	if markErr := d.store.MarkEpisodeEnriched(ctx, episodeID, cause.Error()); markErr != nil {
		return fmt.Errorf("%w (also failed to record enrichment error: %v)", cause, markErr)
	}
	return cause
}

// resolveEntity embeds the entity name (tolerating failure) and inserts or
// resolves it against existing entities
func (d *Dreamer) resolveEntity(ctx context.Context, name, entityType, groupID string) (*models.Entity, error) {
	return d.store.InsertEntity(ctx, &models.Entity{
		CanonicalName:  name,
		EntityType:     entityType,
		Embedding:      d.embed(ctx, name),
		EmbeddingModel: d.embedder.Model(),
		GroupID:        groupID,
	}, entityResolutionThreshold)
}

// embed generates an embedding with a bounded timeout, returning nil on
// failure — embeddings on derived data are best-effort
func (d *Dreamer) embed(ctx context.Context, text string) []float32 {
	embedCtx, cancel := context.WithTimeout(ctx, embedTimeout)
	defer cancel()
	emb, err := d.embedder.Generate(embedCtx, text)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: dreamer failed to generate embedding: %v\n", err)
		return nil
	}
	return emb
}

// buildUserMessage composes the extraction prompt from an episode
func buildUserMessage(ep *models.Episode) string {
	if ep.Name != "" {
		return ep.Name + "\n\n" + ep.Content
	}
	return ep.Content
}

// extractedTriple is one triple as returned by the LLM
type extractedTriple struct {
	Subject     string  `json:"subject"`
	Predicate   string  `json:"predicate"`
	Object      string  `json:"object"`
	SubjectType string  `json:"subject_type"`
	ObjectType  string  `json:"object_type"`
	Confidence  float64 `json:"confidence"`
}

// validateExtraction parses the LLM payload and applies the deterministic
// validation pipeline: predicate whitelist, non-empty subject/object,
// confidence clamped to [0,1] (rejected when not a number), hallucination
// check (subject or object must appear in the episode text), and a cap of
// 10 triples. Rejections are logged, not errors; an error is returned only
// when the envelope itself is unparseable.
func validateExtraction(raw json.RawMessage, episodeText string) ([]extractedTriple, error) {
	var envelope struct {
		Triples []json.RawMessage `json:"triples"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("failed to parse extraction envelope: %w", err)
	}

	haystack := strings.ToLower(episodeText)
	var valid []extractedTriple
	for _, item := range envelope.Triples {
		var tr extractedTriple
		if err := json.Unmarshal(item, &tr); err != nil {
			reject(item, "malformed triple: "+err.Error())
			continue
		}
		if tr.Subject == "" || tr.Object == "" {
			reject(item, "empty subject or object")
			continue
		}
		if !models.ValidPredicates[tr.Predicate] {
			reject(item, fmt.Sprintf("predicate %q not in whitelist", tr.Predicate))
			continue
		}
		if tr.Confidence < 0 {
			tr.Confidence = 0
		}
		if tr.Confidence > 1 {
			tr.Confidence = 1
		}
		if !strings.Contains(haystack, strings.ToLower(tr.Subject)) &&
			!strings.Contains(haystack, strings.ToLower(tr.Object)) {
			reject(item, "neither subject nor object appears in the episode text")
			continue
		}
		valid = append(valid, tr)
		if len(valid) == maxTriplesPerEpisode {
			break
		}
	}
	return valid, nil
}

// reject logs a discarded triple with its reason
func reject(item json.RawMessage, reason string) {
	fmt.Fprintf(os.Stderr, "Warning: dreamer rejected triple %s: %s\n", string(item), reason)
}
