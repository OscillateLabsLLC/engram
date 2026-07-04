package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oscillatelabsllc/engram/internal/db"
	"github.com/oscillatelabsllc/engram/internal/models"
)

// fakeEmbedder is a deterministic in-process Embedder for handler tests
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

func setupMCPServer(t *testing.T) (*Server, *db.Store) {
	t.Helper()
	store, err := db.NewStore(t.TempDir() + "/test.duckdb")
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return NewServer(store, &fakeEmbedder{}), store
}

func callRequest(args map[string]interface{}) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

// resultText extracts the text payload from a tool result, failing on IsError
func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result.IsError {
		t.Fatalf("Tool returned error: %+v", result.Content)
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("Expected text content, got %T", result.Content[0])
	}
	return text.Text
}

// errorText extracts the error text from a tool result, failing when not an error
func errorText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if !result.IsError {
		t.Fatal("Expected tool error result")
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("Expected text content, got %T", result.Content[0])
	}
	return text.Text
}

func TestHandleAddConversation(t *testing.T) {
	t.Run("stores a conversation as one episode", func(t *testing.T) {
		s, store := setupMCPServer(t)
		ctx := context.Background()

		result, err := s.handleAddConversation(ctx, callRequest(map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "What database does Engram use?"},
				map[string]interface{}{"role": "assistant", "content": "Engram uses DuckDB."},
			},
			"source":   "test-client",
			"name":     "db chat",
			"tags":     []interface{}{"conversation"},
			"metadata": `{"session":"abc"}`,
		}))
		if err != nil {
			t.Fatalf("handleAddConversation failed: %v", err)
		}
		var resp struct {
			Success bool   `json:"success"`
			ID      string `json:"id"`
		}
		if err := json.Unmarshal([]byte(resultText(t, result)), &resp); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}
		if !resp.Success || resp.ID == "" {
			t.Fatalf("Expected success with id, got %+v", resp)
		}

		ep, err := store.GetEpisode(ctx, resp.ID)
		if err != nil {
			t.Fatalf("GetEpisode failed: %v", err)
		}
		if !strings.Contains(ep.Content, "user: What database does Engram use?") {
			t.Errorf("Content missing formatted user turn: %q", ep.Content)
		}
		if !strings.Contains(ep.Content, "assistant: Engram uses DuckDB.") {
			t.Errorf("Content missing formatted assistant turn: %q", ep.Content)
		}
		if ep.Name != "db chat" || ep.Source != "test-client" {
			t.Errorf("Wrong name/source: %q / %q", ep.Name, ep.Source)
		}

		var meta map[string]interface{}
		if err := json.Unmarshal([]byte(ep.Metadata), &meta); err != nil {
			t.Fatalf("Metadata not valid JSON: %q", ep.Metadata)
		}
		if meta["session"] != "abc" {
			t.Errorf("Caller metadata not merged: %v", meta)
		}
		msgs, ok := meta["messages"].([]interface{})
		if !ok || len(msgs) != 2 {
			t.Fatalf("Expected raw messages in metadata, got %v", meta["messages"])
		}

		// No triples created
		count, err := store.CountUnenrichedEpisodes(ctx)
		if err != nil {
			t.Fatalf("CountUnenrichedEpisodes failed: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected episode to remain unenriched for the dreamer, got %d", count)
		}
	})

	t.Run("requires messages and source", func(t *testing.T) {
		s, _ := setupMCPServer(t)
		result, _ := s.handleAddConversation(context.Background(), callRequest(map[string]interface{}{
			"source": "test",
		}))
		if msg := errorText(t, result); !strings.Contains(msg, "messages") {
			t.Errorf("Expected messages error, got %q", msg)
		}

		result, _ = s.handleAddConversation(context.Background(), callRequest(map[string]interface{}{
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}))
		if msg := errorText(t, result); !strings.Contains(msg, "source") {
			t.Errorf("Expected source error, got %q", msg)
		}
	})

	t.Run("rejects invalid caller metadata", func(t *testing.T) {
		s, _ := setupMCPServer(t)
		result, _ := s.handleAddConversation(context.Background(), callRequest(map[string]interface{}{
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
			"source":   "test",
			"metadata": "{not json",
		}))
		errorText(t, result)
	})
}

func TestHandleSearchKnowledge(t *testing.T) {
	seedTriple := func(t *testing.T, s *Server, store *db.Store, subject, predicate, object string) {
		t.Helper()
		result, err := s.handleAddKnowledge(context.Background(), callRequest(map[string]interface{}{
			"subject":   subject,
			"predicate": predicate,
			"object":    object,
			"source":    "test",
		}))
		if err != nil {
			t.Fatalf("handleAddKnowledge failed: %v", err)
		}
		resultText(t, result)
	}

	t.Run("finds triples by semantic similarity", func(t *testing.T) {
		s, store := setupMCPServer(t)
		seedTriple(t, s, store, "Mike", "uses", "DuckDB")

		result, err := s.handleSearchKnowledge(context.Background(), callRequest(map[string]interface{}{
			"query": "what tools does Mike use",
		}))
		if err != nil {
			t.Fatalf("handleSearchKnowledge failed: %v", err)
		}
		var triples []models.KnowledgeTriple
		if err := json.Unmarshal([]byte(resultText(t, result)), &triples); err != nil {
			t.Fatalf("Failed to parse triples: %v", err)
		}
		if len(triples) != 1 {
			t.Fatalf("Expected 1 triple, got %d", len(triples))
		}
		if triples[0].SubjectName != "Mike" || triples[0].ObjectName != "DuckDB" {
			t.Errorf("Wrong triple names: %q / %q", triples[0].SubjectName, triples[0].ObjectName)
		}
	})

	t.Run("requires a query", func(t *testing.T) {
		s, _ := setupMCPServer(t)
		result, _ := s.handleSearchKnowledge(context.Background(), callRequest(map[string]interface{}{}))
		errorText(t, result)
	})

	t.Run("rejects injection-shaped group ids", func(t *testing.T) {
		s, _ := setupMCPServer(t)
		result, _ := s.handleSearchKnowledge(context.Background(), callRequest(map[string]interface{}{
			"query":    "anything",
			"group_id": "default' OR '1'='1",
		}))
		if msg := errorText(t, result); !strings.Contains(msg, "group_id") {
			t.Errorf("Expected group_id validation error, got %q", msg)
		}
	})
}

func TestHandleFindLooseEnds(t *testing.T) {
	s, store := setupMCPServer(t)
	ctx := context.Background()

	if err := store.InsertEpisode(ctx, &models.Episode{Content: "lonely", Source: "test"}); err != nil {
		t.Fatalf("InsertEpisode failed: %v", err)
	}

	result, err := s.handleFindLooseEnds(ctx, callRequest(map[string]interface{}{}))
	if err != nil {
		t.Fatalf("handleFindLooseEnds failed: %v", err)
	}
	var res struct {
		UnlinkedEpisodes []models.Episode `json:"unlinked_episodes"`
		DanglingEntities []models.Entity  `json:"dangling_entities"`
		IsolatedClusters [][]string       `json:"isolated_clusters"`
	}
	if err := json.Unmarshal([]byte(resultText(t, result)), &res); err != nil {
		t.Fatalf("Failed to parse loose ends: %v", err)
	}
	if len(res.UnlinkedEpisodes) != 1 {
		t.Errorf("Expected 1 unlinked episode, got %d", len(res.UnlinkedEpisodes))
	}
	if res.DanglingEntities == nil || res.IsolatedClusters == nil {
		t.Error("Expected all three sections present (empty, not null)")
	}
}

func TestHandleSearchGraphDepth(t *testing.T) {
	setup := func(t *testing.T) (*Server, *db.Store, *models.Episode, *models.Episode) {
		s, store := setupMCPServer(t)
		ctx := context.Background()
		ep1 := &models.Episode{Content: "first episode", Source: "test"}
		ep2 := &models.Episode{Content: "second episode", Source: "test"}
		for _, ep := range []*models.Episode{ep1, ep2} {
			if err := store.InsertEpisode(ctx, ep); err != nil {
				t.Fatalf("InsertEpisode failed: %v", err)
			}
		}
		if err := store.InsertEpisodeLink(ctx, &models.EpisodeLink{
			SourceEpisodeID: ep1.ID, TargetEpisodeID: ep2.ID, Relationship: "elaborates",
		}); err != nil {
			t.Fatalf("InsertEpisodeLink failed: %v", err)
		}
		return s, store, ep1, ep2
	}

	t.Run("attaches linked episodes when graph_depth > 0", func(t *testing.T) {
		s, _, ep1, ep2 := setup(t)
		result, err := s.handleSearch(context.Background(), callRequest(map[string]interface{}{
			"graph_depth": 1,
		}))
		if err != nil {
			t.Fatalf("handleSearch failed: %v", err)
		}
		var episodes []struct {
			ID             string `json:"id"`
			LinkedEpisodes []struct {
				EpisodeID    string `json:"episode_id"`
				Relationship string `json:"relationship"`
			} `json:"linked_episodes"`
		}
		if err := json.Unmarshal([]byte(resultText(t, result)), &episodes); err != nil {
			t.Fatalf("Failed to parse search result: %v", err)
		}
		if len(episodes) != 2 {
			t.Fatalf("Expected 2 episodes, got %d", len(episodes))
		}
		found := false
		for _, ep := range episodes {
			if ep.ID != ep1.ID {
				continue
			}
			for _, link := range ep.LinkedEpisodes {
				if link.EpisodeID == ep2.ID && link.Relationship == "elaborates" {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("Expected ep1 to carry a linked episode entry for ep2: %+v", episodes)
		}
	})

	t.Run("keeps plain payload when graph_depth is absent", func(t *testing.T) {
		s, _, _, _ := setup(t)
		result, err := s.handleSearch(context.Background(), callRequest(map[string]interface{}{}))
		if err != nil {
			t.Fatalf("handleSearch failed: %v", err)
		}
		text := resultText(t, result)
		if strings.Contains(text, "linked_episodes") {
			t.Errorf("graph_depth 0 must not change the payload: %s", text)
		}
	})

	t.Run("rejects out-of-range graph_depth", func(t *testing.T) {
		s, _, _, _ := setup(t)
		result, _ := s.handleSearch(context.Background(), callRequest(map[string]interface{}{
			"graph_depth": 5,
		}))
		if msg := errorText(t, result); !strings.Contains(msg, "graph_depth") {
			t.Errorf("Expected graph_depth error, got %q", msg)
		}
	})
}
