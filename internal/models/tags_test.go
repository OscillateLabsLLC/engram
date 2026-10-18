package models

import (
	"reflect"
	"testing"
)

func TestNormalizeTag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"already canonical", "oscillate-labs", "oscillate-labs"},
		{"json-quoted", `"preferences"`, "preferences"},
		{"single-quoted", "'homelab'", "homelab"},
		{"double-wrapped quotes", `"'nested'"`, "nested"},
		{"underscores to dashes", "skill-fallback_llm", "skill-fallback-llm"},
		{"spaces to dashes", "home improvement", "home-improvement"},
		{"uppercase lowered", "MARVIN", "marvin"},
		{"surrounding whitespace", "  ovos  ", "ovos"},
		{"repeated separators collapse", "must__read--now", "must-read-now"},
		{"edge dashes trimmed", "-wip-", "wip"},
		{"unbalanced quote kept", `"open`, `"open`},
		{"dots preserved", "qwen3.5", "qwen3.5"},
		{"empty after quotes", `""`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeTag(tt.input); got != tt.expected {
				t.Errorf("NormalizeTag(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeTags(t *testing.T) {
	t.Run("dedupes after normalization preserving order", func(t *testing.T) {
		got := NormalizeTags([]string{`"project"`, "marvin", "project", "", "Marvin"})
		want := []string{"project", "marvin"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("NormalizeTags = %v, want %v", got, want)
		}
	})

	t.Run("nil stays nil", func(t *testing.T) {
		if got := NormalizeTags(nil); got != nil {
			t.Errorf("NormalizeTags(nil) = %v, want nil", got)
		}
	})
}
