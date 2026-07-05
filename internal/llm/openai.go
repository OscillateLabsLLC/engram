// Package llm provides chat-completion adapters for the dreamer's structured
// extraction calls: an OpenAI-compatible HTTP client and a Claude CLI
// subprocess wrapper.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client handles communication with any OpenAI-compatible chat-completions API
// (Ollama, LM Studio, OpenAI, etc.)
type Client struct {
	endpoint string
	model    string
	apiKey   string
	client   *http.Client
}

// NewClient creates a new chat client for an OpenAI-compatible server.
// baseURL may be a bare host (http://localhost:11434), include the /v1
// prefix (http://localhost:1234/v1), or be a full chat-completions endpoint.
// apiKey is optional; when set it is sent as a Bearer token. timeout <= 0
// defaults to 60s.
func NewClient(baseURL, model, apiKey string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		endpoint: resolveEndpoint(baseURL),
		model:    model,
		apiKey:   apiKey,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Model returns the chat model name this client generates with
func (c *Client) Model() string {
	return c.model
}

// resolveEndpoint normalizes a base URL into a full chat-completions endpoint
func resolveEndpoint(baseURL string) string {
	url := strings.TrimRight(baseURL, "/")
	switch {
	case strings.HasSuffix(url, "/chat/completions"):
		return url
	case strings.HasSuffix(url, "/v1"):
		return url + "/chat/completions"
	default:
		return url + "/v1/chat/completions"
	}
}

// chatMessage matches OpenAI-compatible API format
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest matches OpenAI-compatible API format. ResponseFormat is a
// pointer so the plain-text retry can omit it entirely.
type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

// responseFormat uses the structured-outputs shape ("json_schema") when a
// schema is available — required by LM Studio, supported by OpenAI, Ollama,
// and vLLM — and falls back to "json_object" otherwise.
type responseFormat struct {
	Type       string          `json:"type"`
	JSONSchema *jsonSchemaSpec `json:"json_schema,omitempty"`
}

type jsonSchemaSpec struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
}

// chatResponse matches OpenAI-compatible API format
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// ChatJSON sends a system+user chat completion request in JSON mode and
// returns the model's message content as raw JSON. When a schema is provided
// it is enforced server-side via response_format json_schema and also appended
// to the user message as a hint for servers that ignore the schema field.
//
// Some server/model pairings mishandle structured output (observed: LM Studio
// json_schema against thinking models returns empty content) — on an empty
// response or a response_format rejection, one retry is made in plain-text
// mode, relying on the schema hint in the prompt.
func (c *Client) ChatJSON(ctx context.Context, system, user, schema string) (json.RawMessage, error) {
	format := &responseFormat{Type: "json_object"}
	if schema != "" && json.Valid([]byte(schema)) {
		format = &responseFormat{
			Type:       "json_schema",
			JSONSchema: &jsonSchemaSpec{Name: "result", Schema: json.RawMessage(schema)},
		}
		user = user + "\n\nRespond with JSON matching this schema: " + schema
	}

	messages := []chatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	content, err := c.complete(ctx, chatRequest{Model: c.model, Messages: messages, ResponseFormat: format})
	if shouldRetryPlainText(content, err) {
		content, err = c.complete(ctx, chatRequest{Model: c.model, Messages: messages})
	}
	if err != nil {
		return nil, err
	}

	content = stripJSONFences(content)
	if !json.Valid([]byte(content)) {
		return nil, fmt.Errorf("model returned invalid JSON: %s", truncate(content, 200))
	}
	return json.RawMessage(content), nil
}

// shouldRetryPlainText reports whether a structured-output attempt failed in a
// way that plain-text mode may fix: empty content, or the server rejecting the
// response_format field.
func shouldRetryPlainText(content string, err error) bool {
	if err == nil {
		return content == ""
	}
	return strings.Contains(err.Error(), "response_format")
}

// complete executes one chat-completions request and returns the trimmed
// message content.
func (c *Client) complete(ctx context.Context, reqBody chatRequest) (string, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call chat API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("chat API returned status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices returned")
	}

	return strings.TrimSpace(chatResp.Choices[0].Message.Content), nil
}

// stripJSONFences removes a markdown code fence wrapper if the model emitted
// one around its JSON
func stripJSONFences(content string) string {
	if !strings.HasPrefix(content, "```") {
		return content
	}
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(strings.TrimSpace(content), "```")
	return strings.TrimSpace(content)
}

// truncate shortens s to at most n bytes for error messages
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
