package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func chatOK(content string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": content}},
			},
		})
	}
}

func TestResolveChatEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"bare host (Ollama style)", "http://localhost:11434", "http://localhost:11434/v1/chat/completions"},
		{"trailing slash", "http://localhost:11434/", "http://localhost:11434/v1/chat/completions"},
		{"v1 prefix (LM Studio / OpenAI style)", "http://localhost:1234/v1", "http://localhost:1234/v1/chat/completions"},
		{"v1 prefix with trailing slash", "http://localhost:1234/v1/", "http://localhost:1234/v1/chat/completions"},
		{"full chat completions endpoint", "https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1/chat/completions"},
		{"proxy path with v1", "https://gateway.example.com/openai/v1", "https://gateway.example.com/openai/v1/chat/completions"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveEndpoint(tc.baseURL); got != tc.want {
				t.Errorf("resolveEndpoint(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
}

func TestChatJSON(t *testing.T) {
	t.Run("sends chat completion request and returns JSON content", func(t *testing.T) {
		var gotBody struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			ResponseFormat struct {
				Type string `json:"type"`
			} `json:"response_format"`
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/chat/completions" {
				t.Errorf("Expected path /v1/chat/completions, got %s", r.URL.Path)
			}
			if r.Method != "POST" {
				t.Errorf("Expected POST, got %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("Failed to decode request body: %v", err)
			}
			chatOK(`{"triples":[]}`)(w, r)
		}))
		defer server.Close()

		client := NewClient(server.URL, "test-model", "", 0)
		raw, err := client.ChatJSON(context.Background(), "system prompt", "user text", `{"type":"object"}`)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if string(raw) != `{"triples":[]}` {
			t.Errorf("Unexpected payload: %s", raw)
		}

		if gotBody.Model != "test-model" {
			t.Errorf("Expected model test-model, got %s", gotBody.Model)
		}
		if gotBody.ResponseFormat.Type != "json_object" {
			t.Errorf("Expected response_format json_object, got %q", gotBody.ResponseFormat.Type)
		}
		if len(gotBody.Messages) != 2 {
			t.Fatalf("Expected 2 messages, got %d", len(gotBody.Messages))
		}
		if gotBody.Messages[0].Role != "system" || gotBody.Messages[0].Content != "system prompt" {
			t.Errorf("Bad system message: %+v", gotBody.Messages[0])
		}
		if gotBody.Messages[1].Role != "user" {
			t.Errorf("Expected user role, got %s", gotBody.Messages[1].Role)
		}
		if !strings.Contains(gotBody.Messages[1].Content, "user text") {
			t.Errorf("User message missing user text: %q", gotBody.Messages[1].Content)
		}
		if !strings.Contains(gotBody.Messages[1].Content, `{"type":"object"}`) {
			t.Errorf("User message should append the schema for weak models: %q", gotBody.Messages[1].Content)
		}
	})

	t.Run("omits schema hint when schema is empty", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Messages[1].Content != "just the user text" {
				t.Errorf("Expected unmodified user message, got %q", body.Messages[1].Content)
			}
			chatOK(`{}`)(w, r)
		}))
		defer server.Close()

		client := NewClient(server.URL, "test-model", "", 0)
		if _, err := client.ChatJSON(context.Background(), "sys", "just the user text", ""); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
	})

	t.Run("sends bearer token when API key is set", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer secret" {
				t.Errorf("Expected Authorization 'Bearer secret', got %q", got)
			}
			chatOK(`{}`)(w, r)
		}))
		defer server.Close()

		client := NewClient(server.URL, "test-model", "secret", 0)
		if _, err := client.ChatJSON(context.Background(), "s", "u", ""); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
	})

	t.Run("omits authorization header without API key", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "" {
				t.Errorf("Expected no Authorization header, got %q", got)
			}
			chatOK(`{}`)(w, r)
		}))
		defer server.Close()

		client := NewClient(server.URL, "test-model", "", 0)
		if _, err := client.ChatJSON(context.Background(), "s", "u", ""); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
	})

	t.Run("errors on non-200 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("model not found"))
		}))
		defer server.Close()

		client := NewClient(server.URL, "missing", "", 0)
		_, err := client.ChatJSON(context.Background(), "s", "u", "")
		if err == nil {
			t.Fatal("Expected error for 500 response")
		}
		if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "model not found") {
			t.Errorf("Error should include status and body: %v", err)
		}
	})

	t.Run("errors when content is not valid JSON", func(t *testing.T) {
		server := httptest.NewServer(chatOK("I'm sorry, I can't produce JSON"))
		defer server.Close()

		client := NewClient(server.URL, "test-model", "", 0)
		if _, err := client.ChatJSON(context.Background(), "s", "u", ""); err == nil {
			t.Fatal("Expected error for non-JSON content")
		}
	})

	t.Run("errors when no choices returned", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"choices":[]}`))
		}))
		defer server.Close()

		client := NewClient(server.URL, "test-model", "", 0)
		if _, err := client.ChatJSON(context.Background(), "s", "u", ""); err == nil {
			t.Fatal("Expected error for empty choices")
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(5 * time.Second)
		}))
		defer server.Close()

		client := NewClient(server.URL, "test-model", "", 0)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		if _, err := client.ChatJSON(ctx, "s", "u", ""); err == nil {
			t.Fatal("Expected error due to context timeout")
		}
	})
}

func TestClientModel(t *testing.T) {
	client := NewClient("http://localhost:11434", "qwen3:8b", "", time.Minute)
	if client.Model() != "qwen3:8b" {
		t.Errorf("Expected model qwen3:8b, got %q", client.Model())
	}
}
