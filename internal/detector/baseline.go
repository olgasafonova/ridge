package detector

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// BaselineFileName is the default known-violations file at the repo root.
// The name mirrors dependency-cruiser's .dependency-cruiser-known-violations.json:
// a committed, reviewable record of grandfathered violations.
const BaselineFileName = ".arch-known-violations.json"

// Baseline is a serializable set of known (grandfathered) violations.
// It powers the ratchet mode of arch_validate: existing violations are
// accepted, only new ones fail. This lets a team adopt strict rules on a
// legacy codebase without a red build on day one.
type Baseline struct {
	Version    string          `json:"version"`
	CreatedAt  time.Time       `json:"created_at"`
	Violations []BaselineEntry `json:"violations"`
}

// BaselineEntry identifies one known violation structurally. Matching uses
// Rule+Subject only — Detail carries advisory prose that may be reworded
// between versions and must not invalidate a committed baseline.
type BaselineEntry struct {
	Rule    string `json:"rule"`
	Subject string `json:"subject"`
}

func (e BaselineEntry) key() string { return e.Rule + "|" + e.Subject }

func violationKey(v Violation) string { return v.Rule + "|" + v.Subject }

// NewBaseline builds a baseline from the current violation set, deduplicated
// and sorted so the serialized file diffs cleanly under version control.
func NewBaseline(violations []Violation) *Baseline {
	seen := make(map[string]bool, len(violations))
	entries := make([]BaselineEntry, 0, len(violations))
	for _, v := range violations {
		if seen[violationKey(v)] {
			continue
		}
		seen[violationKey(v)] = true
		entries = append(entries, BaselineEntry{Rule: v.Rule, Subject: v.Subject})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key() < entries[j].key() })

	return &Baseline{
		Version:    "1",
		CreatedAt:  time.Now(),
		Violations: entries,
	}
}

// SaveBaseline serializes a baseline to a JSON file.
func SaveBaseline(path string, b *Baseline) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing baseline: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing baseline file: %w", err)
	}
	return nil
}

// LoadBaseline reads a baseline from a JSON file. Callers should check
// os.IsNotExist on the wrapped error to distinguish "no baseline yet"
// from a corrupt file.
func LoadBaseline(path string) (*Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading baseline file: %w", err)
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parsing baseline: %w", err)
	}
	return &b, nil
}

// SplitKnown partitions violations into fresh (not in the baseline) and a
// count of grandfathered ones. The full violation list stays visible to the
// caller regardless — the baseline changes what fails, never what is shown,
// so a baseline shipped inside a scanned repo can suppress the verdict but
// not hide the findings.
func SplitKnown(violations []Violation, b *Baseline) (fresh []Violation, grandfathered int) {
	known := make(map[string]bool, len(b.Violations))
	for _, e := range b.Violations {
		known[e.key()] = true
	}
	for _, v := range violations {
		if known[violationKey(v)] {
			grandfathered++
			continue
		}
		fresh = append(fresh, v)
	}
	return fresh, grandfathered
}
