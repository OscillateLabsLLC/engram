package mcp

import (
	"strings"
	"testing"
)

func TestNormalizePredicate(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		novel   bool
		wantErr bool
	}{
		{"uses", "uses", false, false},
		{"related_to", "related_to", false, false},
		{"family_of", "family_of", true, false},
		{"mentors", "mentors", true, false},
		{"Family Of", "family_of", true, false},
		{"  depends_on  ", "depends_on", false, false},
		{"works-at", "works_at", false, false},
		{"IS A", "is_a", true, false},
		{"", "", false, true},
		{"9starts_with_digit", "", false, true},
		{"has spaces and $ymbols", "", false, true},
		{"way_too_long_" + strings.Repeat("x", 60), "", false, true},
	}
	for _, c := range cases {
		got, novel, err := normalizePredicate(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizePredicate(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizePredicate(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want || novel != c.novel {
			t.Errorf("normalizePredicate(%q) = (%q, novel=%v), want (%q, novel=%v)", c.in, got, novel, c.want, c.novel)
		}
	}
}
