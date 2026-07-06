package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeStub creates an executable shell script that stands in for the claude
// binary. Returns the script path.
func writeStub(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-stub")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("Failed to write stub: %v", err)
	}
	return path
}

func TestClaudeCLIChatJSON(t *testing.T) {
	t.Run("extracts structured_output from the envelope", func(t *testing.T) {
		stub := writeStub(t, `cat > /dev/null
echo '{"result":"prose answer","structured_output":{"triples":[{"subject":"Mike"}]}}'`)

		cli := NewClaudeCLI(stub)
		raw, err := cli.ChatJSON(context.Background(), "sys", "user", `{"type":"object"}`)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !strings.Contains(string(raw), `"Mike"`) {
			t.Errorf("Expected structured_output payload, got %s", raw)
		}
	})

	t.Run("passes expected arguments and prompt on stdin", func(t *testing.T) {
		dir := t.TempDir()
		argsFile := filepath.Join(dir, "args")
		stdinFile := filepath.Join(dir, "stdin")
		stub := writeStub(t, `printf '%s\n' "$@" > `+argsFile+`
cat > `+stdinFile+`
echo '{"structured_output":{}}'`)

		cli := NewClaudeCLI(stub)
		if _, err := cli.ChatJSON(context.Background(), "SYSTEM PROMPT", "USER PROMPT", `{"my":"schema"}`); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		argsRaw, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("Stub did not record args: %v", err)
		}
		args := strings.Split(strings.TrimRight(string(argsRaw), "\n"), "\n")
		want := []string{"-p", "--tools", "", "--system-prompt", "SYSTEM PROMPT", "--output-format", "json", "--json-schema", `{"my":"schema"}`}
		if len(args) != len(want) {
			t.Fatalf("Expected args %v, got %v", want, args)
		}
		for i := range want {
			if args[i] != want[i] {
				t.Errorf("Arg %d: expected %q, got %q", i, want[i], args[i])
			}
		}

		stdin, err := os.ReadFile(stdinFile)
		if err != nil {
			t.Fatalf("Stub did not record stdin: %v", err)
		}
		if strings.Contains(string(stdin), "SYSTEM PROMPT") {
			t.Errorf("System prompt must be passed via flag, not stdin, got %q", stdin)
		}
		if !strings.Contains(string(stdin), "USER PROMPT") {
			t.Errorf("Stdin should contain the user prompt, got %q", stdin)
		}
	})

	t.Run("disables all tools in the subprocess", func(t *testing.T) {
		dir := t.TempDir()
		argsFile := filepath.Join(dir, "args")
		stub := writeStub(t, `for a in "$@"; do printf '<%s>' "$a"; done > `+argsFile+`
cat > /dev/null
echo '{"structured_output":{}}'`)

		cli := NewClaudeCLI(stub)
		if _, err := cli.ChatJSON(context.Background(), "s", "u", "{}"); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		argsRaw, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("Stub did not record args: %v", err)
		}
		if !strings.Contains(string(argsRaw), "<--tools><>") {
			t.Errorf("Expected --tools with empty value (all tools disabled), got %s", argsRaw)
		}
	})

	t.Run("parses event-array envelope from CLI 2.1.x", func(t *testing.T) {
		stub := writeStub(t, `cat > /dev/null
echo '[{"type":"system","model":"x"},{"type":"assistant","message":{}},{"type":"result","is_error":false,"result":"prose","structured_output":{"triples":[{"subject":"Engram"}]}}]'`)

		cli := NewClaudeCLI(stub)
		raw, err := cli.ChatJSON(context.Background(), "s", "u", "{}")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !strings.Contains(string(raw), `"Engram"`) {
			t.Errorf("Expected structured_output from result event, got %s", raw)
		}
	})

	t.Run("errors on event array without result item", func(t *testing.T) {
		stub := writeStub(t, `cat > /dev/null
echo '[{"type":"system"},{"type":"assistant"}]'`)

		cli := NewClaudeCLI(stub)
		if _, err := cli.ChatJSON(context.Background(), "s", "u", "{}"); err == nil {
			t.Fatal("Expected error for event array without result item")
		}
	})

	t.Run("falls back to parsing result as JSON", func(t *testing.T) {
		stub := writeStub(t, `cat > /dev/null
echo '{"result":"{\"ok\":true}"}'`)

		cli := NewClaudeCLI(stub)
		raw, err := cli.ChatJSON(context.Background(), "s", "u", "{}")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if strings.TrimSpace(string(raw)) != `{"ok":true}` {
			t.Errorf("Expected result JSON, got %s", raw)
		}
	})

	t.Run("falls back to result when structured_output is null", func(t *testing.T) {
		stub := writeStub(t, `cat > /dev/null
echo '{"result":"{\"ok\":1}","structured_output":null}'`)

		cli := NewClaudeCLI(stub)
		raw, err := cli.ChatJSON(context.Background(), "s", "u", "{}")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if strings.TrimSpace(string(raw)) != `{"ok":1}` {
			t.Errorf("Expected result JSON, got %s", raw)
		}
	})

	t.Run("errors when result is not JSON and no structured output", func(t *testing.T) {
		stub := writeStub(t, `cat > /dev/null
echo '{"result":"plain prose, no json here"}'`)

		cli := NewClaudeCLI(stub)
		if _, err := cli.ChatJSON(context.Background(), "s", "u", "{}"); err == nil {
			t.Fatal("Expected error for non-JSON result")
		}
	})

	t.Run("errors on nonzero exit including stderr", func(t *testing.T) {
		stub := writeStub(t, `cat > /dev/null
echo "authentication broke" >&2
exit 1`)

		cli := NewClaudeCLI(stub)
		_, err := cli.ChatJSON(context.Background(), "s", "u", "{}")
		if err == nil {
			t.Fatal("Expected error for nonzero exit")
		}
		if !strings.Contains(err.Error(), "authentication broke") {
			t.Errorf("Error should include stderr, got: %v", err)
		}
	})

	t.Run("surfaces stdout detail when stderr is empty on failure", func(t *testing.T) {
		stub := writeStub(t, `cat > /dev/null
echo '{"type":"result","is_error":true,"result":"Rate limit reached. Try again later."}'
exit 1`)

		cli := NewClaudeCLI(stub)
		_, err := cli.ChatJSON(context.Background(), "s", "u", "{}")
		if err == nil {
			t.Fatal("Expected error for nonzero exit")
		}
		if !strings.Contains(err.Error(), "Rate limit reached") {
			t.Errorf("Error should surface stdout detail, got: %v", err)
		}
	})

	t.Run("errors on unparseable stdout", func(t *testing.T) {
		stub := writeStub(t, `cat > /dev/null
echo 'total garbage'`)

		cli := NewClaudeCLI(stub)
		if _, err := cli.ChatJSON(context.Background(), "s", "u", "{}"); err == nil {
			t.Fatal("Expected error for garbage stdout")
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		stub := writeStub(t, `sleep 5
echo '{"structured_output":{}}'`)

		cli := NewClaudeCLI(stub)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if _, err := cli.ChatJSON(ctx, "s", "u", "{}"); err == nil {
			t.Fatal("Expected error due to context timeout")
		}
	})
}

func TestClaudeCLIDefaults(t *testing.T) {
	cli := NewClaudeCLI("")
	if cli.binary != "claude" {
		t.Errorf("Expected default binary 'claude', got %q", cli.binary)
	}
	if cli.Model() != "claude-cli" {
		t.Errorf("Expected model 'claude-cli', got %q", cli.Model())
	}
}
