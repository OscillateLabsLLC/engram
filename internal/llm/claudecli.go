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

// cliEnvelope is one JSON object printed by `claude -p --output-format json`.
// Depending on CLI version the top level is either a single result object or
// an array of event objects where the final `type: "result"` item carries the
// structured output.
type cliEnvelope struct {
	Type             string          `json:"type"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

// ChatJSON invokes the CLI in print mode with a JSON schema and returns the
// structured output. The subprocess runs with ALL tools disabled — episode
// content is untrusted input, and an extraction call must not be able to read
// files or run commands regardless of what that content asks for. The system
// prompt is passed via --system-prompt so untrusted content on stdin cannot
// displace the extraction instructions.
func (c *ClaudeCLI) ChatJSON(ctx context.Context, system, user, schema string) (json.RawMessage, error) {
	cmd := exec.CommandContext(ctx, c.binary,
		"-p",
		"--tools", "",
		"--system-prompt", system,
		"--output-format", "json",
		"--json-schema", schema,
	)
	cmd.Stdin = strings.NewReader(user)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// The CLI often reports errors (rate limits, auth) in its JSON on
		// stdout with an empty stderr — surface whichever has content
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = truncate(strings.TrimSpace(stdout.String()), 300)
		}
		return nil, fmt.Errorf("claude CLI failed: %w (output: %s)", err, detail)
	}

	envelope, err := parseCLIOutput(stdout.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
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

// parseCLIOutput handles both envelope shapes: a single result object (as
// documented) or an array of event objects (observed on CLI 2.1.x), where the
// result payload is the item with type "result".
func parseCLIOutput(out []byte) (*cliEnvelope, error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var events []cliEnvelope
		if err := json.Unmarshal(trimmed, &events); err != nil {
			return nil, fmt.Errorf("failed to parse claude CLI event array: %w", err)
		}
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Type == "result" {
				return &events[i], nil
			}
		}
		return nil, fmt.Errorf("claude CLI event array contained no result item")
	}

	var envelope cliEnvelope
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return nil, fmt.Errorf("failed to parse claude CLI output: %w", err)
	}
	return &envelope, nil
}
