package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ClaudeCLI runs prompts through the Claude Code CLI as a subprocess. Useful
// when a Claude subscription (or ANTHROPIC_API_KEY) is already configured for
// the `claude` binary and no separate OpenAI-compatible server is running.
type ClaudeCLI struct {
	binary string
}

// NewClaudeCLI creates a Claude CLI adapter. binary is the executable to
// invoke; empty defaults to "claude".
func NewClaudeCLI(binary string) *ClaudeCLI {
	if binary == "" {
		binary = "claude"
	}
	return &ClaudeCLI{binary: binary}
}

// Model returns a fixed identifier for provenance stamping — the CLI picks
// the underlying model from its own configuration.
func (c *ClaudeCLI) Model() string {
	return "claude-cli"
}

// cliEnvelope is the JSON envelope `claude -p --output-format json` prints
type cliEnvelope struct {
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

// ChatJSON invokes the CLI in print mode with a JSON schema and returns the
// structured output. Falls back to parsing the result field as JSON when the
// envelope carries no structured_output.
func (c *ClaudeCLI) ChatJSON(ctx context.Context, system, user, schema string) (json.RawMessage, error) {
	cmd := exec.CommandContext(ctx, c.binary, "-p", "--output-format", "json", "--json-schema", schema)
	cmd.Stdin = strings.NewReader(system + "\n\n" + user)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("claude CLI failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	var envelope cliEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		return nil, fmt.Errorf("failed to parse claude CLI output: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	if len(envelope.StructuredOutput) > 0 && string(envelope.StructuredOutput) != "null" {
		return envelope.StructuredOutput, nil
	}

	result := strings.TrimSpace(envelope.Result)
	if result == "" || !json.Valid([]byte(result)) {
		return nil, fmt.Errorf("claude CLI returned no structured output and result is not valid JSON: %s", truncate(result, 200))
	}
	return json.RawMessage(result), nil
}
