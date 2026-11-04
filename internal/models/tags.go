package models

import (
	"regexp"
	"strings"
)

var (
	tagSeparators = regexp.MustCompile(`[\s_]+`)
	repeatedDash  = regexp.MustCompile(`-{2,}`)
)

const tagQuoteChars = "\"'`"

// NormalizeTag converts a tag to its canonical form: lowercase kebab-case with
// surrounding quotes removed. Callers sometimes send JSON-encoded strings as tags
// ("\"project\""), which otherwise become distinct tags that never match a filter.
func NormalizeTag(tag string) string {
	t := strings.TrimSpace(tag)
	for len(t) >= 2 && t[0] == t[len(t)-1] && strings.IndexByte(tagQuoteChars, t[0]) >= 0 {
		t = strings.TrimSpace(t[1 : len(t)-1])
	}
	t = tagSeparators.ReplaceAllString(strings.ToLower(t), "-")
	return strings.Trim(repeatedDash.ReplaceAllString(t, "-"), "-")
}

// NormalizeTags canonicalizes each tag, dropping empties and duplicates while
// preserving first-seen order.
func NormalizeTags(tags []string) []string {
	if tags == nil {
		return nil
	}
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		n := NormalizeTag(tag)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}
