package mcp

import (
	"fmt"
	"regexp"
	"strings"
)

// corePredicates is the preferred relationship vocabulary. It guides agents
// toward consistent, traversable graph edges but does NOT gate writes:
// assertion-time knowledge keeps its nuance ("family_of", "mentors"), while
// the automated extraction pipeline separately constrains itself to this
// list to prevent free-text predicate explosion.
var corePredicates = map[string]bool{
	"owns": true, "works_at": true, "contributes_to": true, "uses": true,
	"prefers": true, "builds": true, "depends_on": true, "located_in": true,
	"related_to": true, "part_of": true, "instance_of": true, "created_by": true,
	"configured_with": true, "deployed_on": true, "communicates_via": true,
}

// predicateShape enforces lowercase snake_case after normalization so the
// graph never accumulates casing/spacing variants of the same relationship
var predicateShape = regexp.MustCompile(`^[a-z][a-z0-9_]{0,49}$`)

// normalizePredicate lowercases and snake_cases the input, validates its
// shape, and reports whether it falls outside the core vocabulary
func normalizePredicate(raw string) (predicate string, novel bool, err error) {
	predicate = strings.ToLower(strings.TrimSpace(raw))
	predicate = strings.Join(strings.Fields(predicate), "_") // spaces → single underscores
	predicate = strings.ReplaceAll(predicate, "-", "_")
	if !predicateShape.MatchString(predicate) {
		return "", false, fmt.Errorf("invalid predicate %q: must normalize to lowercase snake_case (letters, digits, underscores; max 50 chars)", raw)
	}
	return predicate, !corePredicates[predicate], nil
}
