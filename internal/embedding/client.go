package embedding

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

// Client handles communication with any OpenAI-compatible embeddings API
// (Ollama, LM Studio, OpenAI, etc.)
type Client struct {
	endpoint string
	model    string
	apiKey   string
	client   *http.Client
}

// NewClient creates a new embedding client for an OpenAI-compatible server.
// baseURL may be a bare host (http://localhost:11434), include the /v1
// prefix (http://localhost:1234/v1), or be a full embeddings endpoint
// (https://api.openai.com/v1/embeddings). apiKey is optional; when set it
// is sent as a Bearer token.
func NewClient(baseURL, model, apiKey string) *Client {
	return &Client{
		endpoint: resolveEndpoint(baseURL),
		model:    model,
		apiKey:   apiKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// resolveEndpoint normalizes a base URL into a full embeddings endpoint
func resolveEndpoint(baseURL string) string {
	url := strings.TrimRight(baseURL, "/")
	switch {
	case strings.HasSuffix(url, "/embeddings"):
		return url
	case strings.HasSuffix(url, "/v1"):
		return url + "/embeddings"
	default:
		return url + "/v1/embeddings"
	}
}

// embedRequest matches OpenAI-compatible API format
type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// embedResponse matches OpenAI-compatible API format
type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Generate creates an embedding for the given text
func (c *Client) Generate(ctx context.Context, text string) ([]float32, error) {
	reqBody := embedRequest{
		Model: c.model,
		Input: text,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call embedding API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embedding API returned status %d: %s", resp.StatusCode, string(body))
	}

	var embedResp embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(embedResp.Data) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return embedResp.Data[0].Embedding, nil
}
