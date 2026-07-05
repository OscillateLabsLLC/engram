package dreamer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/models"
)

// fakeLLM is a deterministic in-process LLM for dreamer tests
type fakeLLM struct {
	mu         sync.Mutex
	response   json.RawMessage
	err        error
	calls      int
	lastSystem string
	lastUser   string
	lastSchema string
}

func (f *fakeLLM) ChatJSON(ctx context.Context, system, user, schema string) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastSystem = system
	f.lastUser = user
	f.lastSchema = schema
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func (f *fakeLLM) Model() string { return "fake-model" }

// fakeEmbedder is a deterministic in-process Embedder for dreamer tests
type fakeEmbedder struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (f *fakeEmbedder) Generate(ctx context.Context, text string) ([]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	emb := make([]float32, 768)
	emb[0] = float32(len(text))
	return emb, nil
}

func (f *fakeEmbedder) Model() string { return "fake-embed" }

func setupTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.NewStore(t.TempDir() + "/test.duckdb")
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func insertEpisode(t *testing.T, store *db.Store, ep *models.Episode) *models.Episode {
	t.Helper()
	if err := store.InsertEpisode(context.Background(), ep); err != nil {
		t.Fatalf("Failed to insert episode: %v", err)
	}
	return ep
}

// searchTriples fetches all knowledge triples via vector search — the fake
// embedder maps every text onto the same axis so similarity is always 1.0
func searchTriples(t *testing.T, store *db.Store) []models.KnowledgeTriple {
	t.Helper()
	emb := make([]float32, 768)
	emb[0] = 1
	triples, err := store.SearchKnowledge(context.Background(), emb, "", 100, 0.5)
	if err != nil {
		t.Fatalf("SearchKnowledge failed: %v", err)
	}
	return triples
}

func triplesJSON(triples ...string) json.RawMessage {
	return json.RawMessage(`{"triples":[` + strings.Join(triples, ",") + `]}`)
}

func TestProcessEpisode(t *testing.T) {
	t.Run("extracts triples into the knowledge graph", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{response: triplesJSON(
			`{"subject":"Mike","predicate":"uses","object":"DuckDB","subject_type":"person","object_type":"tool","confidence":0.9}`,
		)}
		d := New(store, llm, &fakeEmbedder{}, time.Second)
		ep := insertEpisode(t, store, &models.Episode{Content: "Mike uses DuckDB for Engram storage", Name: "storage note", Source: "test"})

		if err := d.ProcessEpisode(context.Background(), ep); err != nil {
			t.Fatalf("ProcessEpisode failed: %v", err)
		}

		triples := searchTriples(t, store)
		if len(triples) != 1 {
			t.Fatalf("Expected 1 triple, got %d", len(triples))
		}
		tr := triples[0]
		if tr.SubjectName != "Mike" || tr.Predicate != "uses" || tr.ObjectName != "DuckDB" {
			t.Errorf("Wrong triple: %s %s %s", tr.SubjectName, tr.Predicate, tr.ObjectName)
		}
		if tr.Source != "dreamer/fake-model" {
			t.Errorf("Expected source dreamer/fake-model, got %q", tr.Source)
		}
		if tr.Verified {
			t.Error("Dreamer triples must not be verified")
		}
		if tr.Confidence < 0.89 || tr.Confidence > 0.91 {
			t.Errorf("Expected confidence 0.9, got %f", tr.Confidence)
		}
		if tr.SourceEpisodeID != ep.ID {
			t.Errorf("Expected source episode %s, got %s", ep.ID, tr.SourceEpisodeID)
		}

		// Episode is stamped enriched
		count, err := store.CountUnenrichedEpisodes(context.Background())
		if err != nil {
			t.Fatalf("CountUnenrichedEpisodes failed: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected episode stamped enriched, %d remain", count)
		}

		// Prompt content sanity
		if !strings.Contains(llm.lastUser, "Mike uses DuckDB") {
			t.Errorf("User prompt should contain episode content: %q", llm.lastUser)
		}
		if !strings.Contains(llm.lastUser, "storage note") {
			t.Errorf("User prompt should contain episode name: %q", llm.lastUser)
		}
		if !strings.Contains(llm.lastSystem, "knowledge triples") {
			t.Errorf("Unexpected system prompt: %q", llm.lastSystem)
		}
		if !strings.Contains(llm.lastSchema, "triples") {
			t.Errorf("Schema should describe triples envelope: %q", llm.lastSchema)
		}
	})

	t.Run("clamps confidence into [0,1]", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{response: triplesJSON(
			`{"subject":"Mike","predicate":"uses","object":"DuckDB","confidence":3.5}`,
		)}
		d := New(store, llm, &fakeEmbedder{}, time.Second)
		ep := insertEpisode(t, store, &models.Episode{Content: "Mike uses DuckDB", Source: "test"})

		if err := d.ProcessEpisode(context.Background(), ep); err != nil {
			t.Fatalf("ProcessEpisode failed: %v", err)
		}
		triples := searchTriples(t, store)
		if len(triples) != 1 {
			t.Fatalf("Expected 1 triple, got %d", len(triples))
		}
		if triples[0].Confidence != 1.0 {
			t.Errorf("Expected confidence clamped to 1.0, got %f", triples[0].Confidence)
		}
	})

	t.Run("rejects invalid triples deterministically", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{response: triplesJSON(
			`{"subject":"Mike","predicate":"annihilates","object":"DuckDB","confidence":0.9}`, // bad predicate
			`{"subject":"","predicate":"uses","object":"DuckDB","confidence":0.9}`,            // empty subject
			`{"subject":"Mike","predicate":"uses","object":"","confidence":0.9}`,              // empty object
			`{"subject":"Zeus","predicate":"uses","object":"Olympus","confidence":0.9}`,       // hallucination
			`{"subject":"Mike","predicate":"uses","object":"DuckDB","confidence":"high"}`,     // non-numeric confidence
			`{"subject":"Mike","predicate":"prefers","object":"DuckDB","confidence":0.7}`,     // valid
		)}
		d := New(store, llm, &fakeEmbedder{}, time.Second)
		ep := insertEpisode(t, store, &models.Episode{Content: "Mike prefers DuckDB", Source: "test"})

		if err := d.ProcessEpisode(context.Background(), ep); err != nil {
			t.Fatalf("ProcessEpisode failed: %v", err)
		}
		triples := searchTriples(t, store)
		if len(triples) != 1 {
			t.Fatalf("Expected only the valid triple, got %d", len(triples))
		}
		if triples[0].Predicate != "prefers" {
			t.Errorf("Wrong surviving triple: %+v", triples[0])
		}
	})

	t.Run("keeps triples where either subject or object appears in text", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{response: triplesJSON(
			`{"subject":"Oscillate Labs","predicate":"owns","object":"Engram","confidence":0.8}`,
		)}
		d := New(store, llm, &fakeEmbedder{}, time.Second)
		// Only "Engram" appears in the text; subject is implied
		ep := insertEpisode(t, store, &models.Episode{Content: "engram is the memory project", Source: "test"})

		if err := d.ProcessEpisode(context.Background(), ep); err != nil {
			t.Fatalf("ProcessEpisode failed: %v", err)
		}
		if got := len(searchTriples(t, store)); got != 1 {
			t.Errorf("Expected 1 triple (object matched case-insensitively), got %d", got)
		}
	})

	t.Run("caps triples at 10 per episode", func(t *testing.T) {
		store := setupTestStore(t)
		var many []string
		for i := 0; i < 15; i++ {
			many = append(many, fmt.Sprintf(`{"subject":"Mike","predicate":"uses","object":"tool%d","confidence":0.9}`, i))
		}
		llm := &fakeLLM{response: triplesJSON(many...)}
		d := New(store, llm, &fakeEmbedder{}, time.Second)
		content := "Mike uses"
		for i := 0; i < 15; i++ {
			content += fmt.Sprintf(" tool%d", i)
		}
		ep := insertEpisode(t, store, &models.Episode{Content: content, Source: "test"})

		if err := d.ProcessEpisode(context.Background(), ep); err != nil {
			t.Fatalf("ProcessEpisode failed: %v", err)
		}
		if got := len(searchTriples(t, store)); got != 10 {
			t.Errorf("Expected 10 triples (capped), got %d", got)
		}
	})

	t.Run("leaves episode unenriched on LLM failure", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{err: errors.New("connection refused")}
		d := New(store, llm, &fakeEmbedder{}, time.Second)
		ep := insertEpisode(t, store, &models.Episode{
			Content: "transient", Source: "test", Metadata: `{"kept":"yes"}`,
		})

		err := d.ProcessEpisode(context.Background(), ep)
		if !errors.Is(err, ErrLLMFailure) {
			t.Fatalf("Expected ErrLLMFailure, got %v", err)
		}

		count, _ := store.CountUnenrichedEpisodes(context.Background())
		if count != 1 {
			t.Error("LLM-call failure must leave the episode queued for a later run")
		}
		got, err := store.GetEpisode(context.Background(), ep.ID)
		if err != nil {
			t.Fatalf("GetEpisode failed: %v", err)
		}
		if got.Metadata != `{"kept":"yes"}` {
			t.Errorf("Metadata must be untouched on transient failure, got %q", got.Metadata)
		}
	})

	t.Run("stamps enriched with error on unparseable payload", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{response: json.RawMessage(`"not an object"`)}
		d := New(store, llm, &fakeEmbedder{}, time.Second)
		ep := insertEpisode(t, store, &models.Episode{
			Content: "whatever", Source: "test", Metadata: `{"kept":"yes"}`,
		})

		err := d.ProcessEpisode(context.Background(), ep)
		if err == nil {
			t.Fatal("Expected error from unparseable payload")
		}
		if errors.Is(err, ErrLLMFailure) {
			t.Error("Parse failure must not be classified as an LLM-call failure")
		}
		count, _ := store.CountUnenrichedEpisodes(context.Background())
		if count != 0 {
			t.Error("Unparseable episode must still be stamped enriched (poison pill protection)")
		}

		got, err := store.GetEpisode(context.Background(), ep.ID)
		if err != nil {
			t.Fatalf("GetEpisode failed: %v", err)
		}
		var meta map[string]interface{}
		if err := json.Unmarshal([]byte(got.Metadata), &meta); err != nil {
			t.Fatalf("Metadata not valid JSON: %q", got.Metadata)
		}
		if meta["kept"] != "yes" {
			t.Error("Existing metadata clobbered")
		}
		if msg, _ := meta["enrichment_error"].(string); !strings.Contains(msg, "invalid") {
			t.Errorf("Expected enrichment_error with cause, got %v", meta["enrichment_error"])
		}
	})

	t.Run("tolerates embedding failure", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{response: triplesJSON(
			`{"subject":"Mike","predicate":"uses","object":"DuckDB","confidence":0.9}`,
		)}
		d := New(store, llm, &fakeEmbedder{err: errors.New("embedder down")}, time.Second)
		ep := insertEpisode(t, store, &models.Episode{Content: "Mike uses DuckDB", Source: "test"})

		if err := d.ProcessEpisode(context.Background(), ep); err != nil {
			t.Fatalf("ProcessEpisode should tolerate embed failure: %v", err)
		}
		// Triple exists but has no embedding — verify via entity lookup instead
		entity, err := store.InsertEntity(context.Background(), &models.Entity{CanonicalName: "Mike"}, 0.88)
		if err != nil {
			t.Fatalf("InsertEntity failed: %v", err)
		}
		shared, err := store.FindEpisodesSharingEntities(context.Background(), []string{entity.ID}, "")
		if err != nil {
			t.Fatalf("FindEpisodesSharingEntities failed: %v", err)
		}
		if len(shared) != 1 || shared[0] != ep.ID {
			t.Errorf("Expected triple written without embedding, got episodes %v", shared)
		}
	})

	t.Run("links episodes sharing entities", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{response: triplesJSON(
			`{"subject":"Mike","predicate":"uses","object":"DuckDB","confidence":0.9}`,
		)}
		d := New(store, llm, &fakeEmbedder{}, time.Second)

		ep1 := insertEpisode(t, store, &models.Episode{Content: "Mike uses DuckDB", Source: "test"})
		ep2 := insertEpisode(t, store, &models.Episode{Content: "Mike uses DuckDB again", Source: "test"})

		if err := d.ProcessEpisode(context.Background(), ep1); err != nil {
			t.Fatalf("ProcessEpisode ep1 failed: %v", err)
		}
		if err := d.ProcessEpisode(context.Background(), ep2); err != nil {
			t.Fatalf("ProcessEpisode ep2 failed: %v", err)
		}

		links, err := store.GetEpisodeLinks(context.Background(), ep2.ID)
		if err != nil {
			t.Fatalf("GetEpisodeLinks failed: %v", err)
		}
		if len(links) == 0 {
			t.Fatal("Expected episode link between episodes sharing entities")
		}
		link := links[0]
		if link.Relationship != "same_entity" {
			t.Errorf("Expected same_entity relationship, got %q", link.Relationship)
		}
		if link.ViaEntityID == "" {
			t.Error("Expected via_entity_id to be set")
		}
		if link.SourceEpisodeID != ep2.ID || link.TargetEpisodeID != ep1.ID {
			t.Errorf("Wrong link direction: %s -> %s", link.SourceEpisodeID, link.TargetEpisodeID)
		}
	})

	t.Run("empty triples list still enriches", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{response: json.RawMessage(`{"triples":[]}`)}
		d := New(store, llm, &fakeEmbedder{}, time.Second)
		insertEpisode(t, store, &models.Episode{Content: "nothing factual here", Source: "test"})

		if err := d.Process(context.Background(), nil); err != nil {
			t.Fatalf("Process failed: %v", err)
		}
		count, _ := store.CountUnenrichedEpisodes(context.Background())
		if count != 0 {
			t.Errorf("Expected all episodes enriched, %d remain", count)
		}
	})
}

func TestProcess(t *testing.T) {
	t.Run("processes all unenriched episodes and reports progress", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{response: json.RawMessage(`{"triples":[]}`)}
		d := New(store, llm, &fakeEmbedder{}, time.Second)

		for i := 0; i < 3; i++ {
			insertEpisode(t, store, &models.Episode{Content: fmt.Sprintf("episode %d", i), Source: "test"})
		}

		var done, failed int
		err := d.Process(context.Background(), func(f bool) {
			done++
			if f {
				failed++
			}
		})
		if err != nil {
			t.Fatalf("Process failed: %v", err)
		}
		if done != 3 || failed != 0 {
			t.Errorf("Expected 3 done / 0 failed, got %d/%d", done, failed)
		}

		// Second run is a no-op
		llm.mu.Lock()
		callsBefore := llm.calls
		llm.mu.Unlock()
		if err := d.Process(context.Background(), nil); err != nil {
			t.Fatalf("Second Process failed: %v", err)
		}
		llm.mu.Lock()
		if llm.calls != callsBefore {
			t.Errorf("Second run should not call the LLM, got %d extra calls", llm.calls-callsBefore)
		}
		llm.mu.Unlock()
	})

	t.Run("counts failures below the breaker threshold and keeps going", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{err: errors.New("llm down")}
		d := New(store, llm, &fakeEmbedder{}, time.Second)

		insertEpisode(t, store, &models.Episode{Content: "one", Source: "test"})
		insertEpisode(t, store, &models.Episode{Content: "two", Source: "test"})

		var done, failed int
		err := d.Process(context.Background(), func(f bool) {
			done++
			if f {
				failed++
			}
		})
		if err != nil {
			t.Fatalf("Two failures are below the breaker threshold, should not be fatal: %v", err)
		}
		if done != 2 || failed != 2 {
			t.Errorf("Expected 2 done / 2 failed, got %d/%d", done, failed)
		}
		count, _ := store.CountUnenrichedEpisodes(context.Background())
		if count != 2 {
			t.Errorf("LLM-failed episodes must stay queued for retry, got %d remaining", count)
		}
	})

	t.Run("aborts after consecutive LLM failures", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{err: errors.New("llm down")}
		d := New(store, llm, &fakeEmbedder{}, time.Second)

		for i := 0; i < maxConsecutiveLLMFailures+3; i++ {
			insertEpisode(t, store, &models.Episode{Content: fmt.Sprintf("ep %d", i), Source: "test"})
		}

		var done int
		err := d.Process(context.Background(), func(bool) { done++ })
		if err == nil {
			t.Fatal("Expected crawl abort after consecutive LLM failures")
		}
		if !errors.Is(err, ErrLLMFailure) {
			t.Errorf("Abort error should wrap ErrLLMFailure, got %v", err)
		}
		if done != maxConsecutiveLLMFailures {
			t.Errorf("Expected exactly %d attempts before abort, got %d", maxConsecutiveLLMFailures, done)
		}
		count, _ := store.CountUnenrichedEpisodes(context.Background())
		if count != maxConsecutiveLLMFailures+3 {
			t.Errorf("All episodes must remain queued after abort, got %d", count)
		}
	})

	t.Run("stops on context cancellation", func(t *testing.T) {
		store := setupTestStore(t)
		llm := &fakeLLM{response: json.RawMessage(`{"triples":[]}`)}
		d := New(store, llm, &fakeEmbedder{}, time.Second)
		insertEpisode(t, store, &models.Episode{Content: "one", Source: "test"})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := d.Process(ctx, nil); err == nil {
			t.Error("Expected context error")
		}
	})
}

func TestProbe(t *testing.T) {
	t.Run("succeeds when LLM answers", func(t *testing.T) {
		d := New(setupTestStore(t), &fakeLLM{response: json.RawMessage(`{"ok":true}`)}, &fakeEmbedder{}, time.Second)
		if err := d.Probe(context.Background()); err != nil {
			t.Errorf("Probe failed: %v", err)
		}
	})

	t.Run("propagates LLM errors", func(t *testing.T) {
		d := New(setupTestStore(t), &fakeLLM{err: errors.New("unreachable")}, &fakeEmbedder{}, time.Second)
		if err := d.Probe(context.Background()); err == nil {
			t.Error("Expected probe error")
		}
	})
}
