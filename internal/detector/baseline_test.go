package detector

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func TestBaseline_RoundTrip(t *testing.T) {
	violations := []Violation{
		{Rule: "no_circular_dependencies", Severity: model.SeverityCritical, Subject: "cycle(2 nodes): pkg:a, pkg:b", Detail: "Circular dependency among: [pkg:a pkg:b]"},
		{Rule: "no_orphan_nodes", Severity: model.SeverityLow, Subject: "pkg:orphan", Detail: "Node has no connections"},
	}

	file := filepath.Join(t.TempDir(), BaselineFileName)
	if err := SaveBaseline(file, NewBaseline(violations)); err != nil {
		t.Fatalf("saving baseline: %v", err)
	}

	loaded, err := LoadBaseline(file)
	if err != nil {
		t.Fatalf("loading baseline: %v", err)
	}
	if len(loaded.Violations) != 2 {
		t.Fatalf("expected 2 baseline entries, got %d", len(loaded.Violations))
	}
	if loaded.Version != "1" {
		t.Fatalf("expected version 1, got %q", loaded.Version)
	}
}

func TestNewBaseline_DedupsAndSorts(t *testing.T) {
	violations := []Violation{
		{Rule: "no_orphan_nodes", Subject: "pkg:z"},
		{Rule: "no_orphan_nodes", Subject: "pkg:a"},
		{Rule: "no_orphan_nodes", Subject: "pkg:z"}, // duplicate
	}

	b := NewBaseline(violations)

	if len(b.Violations) != 2 {
		t.Fatalf("expected 2 deduped entries, got %d", len(b.Violations))
	}
	if b.Violations[0].Subject != "pkg:a" || b.Violations[1].Subject != "pkg:z" {
		t.Fatalf("expected sorted entries [pkg:a pkg:z], got %+v", b.Violations)
	}
}

func TestSplitKnown_RatchetSemantics(t *testing.T) {
	grandfathered := Violation{Rule: "no_orphan_nodes", Subject: "pkg:old", Detail: "old orphan"}
	fresh := Violation{Rule: "no_orphan_nodes", Subject: "pkg:new", Detail: "new orphan"}
	baseline := NewBaseline([]Violation{grandfathered})

	newViolations, known := SplitKnown([]Violation{grandfathered, fresh}, baseline)

	if known != 1 {
		t.Fatalf("expected 1 grandfathered violation, got %d", known)
	}
	if len(newViolations) != 1 || newViolations[0].Subject != "pkg:new" {
		t.Fatalf("expected only pkg:new to be fresh, got %+v", newViolations)
	}
}

// TestSplitKnown_DetailChangeStillMatches verifies matching keys on
// Rule+Subject only: rewording Detail between versions must not invalidate
// a committed baseline.
func TestSplitKnown_DetailChangeStillMatches(t *testing.T) {
	baseline := NewBaseline([]Violation{{Rule: "no_orphan_nodes", Subject: "pkg:x", Detail: "old wording"}})

	fresh, known := SplitKnown([]Violation{{Rule: "no_orphan_nodes", Subject: "pkg:x", Detail: "completely new wording"}}, baseline)

	if known != 1 || len(fresh) != 0 {
		t.Fatalf("expected detail-only change to stay grandfathered, got fresh=%+v known=%d", fresh, known)
	}
}

func TestLoadBaseline_MissingFile(t *testing.T) {
	_, err := LoadBaseline(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("expected error for missing baseline file")
	}
}

// TestCycleViolation_DeterministicSubject verifies the cycle Subject is
// stable across runs and structurally identifies the cycle membership —
// the property baseline matching depends on. Two different cycles of the
// same size must produce different subjects.
func TestCycleViolation_DeterministicSubject(t *testing.T) {
	makeGraph := func(a, b string) *model.ArchGraph {
		g := model.NewGraph(filepath.Join(os.TempDir(), "nonexistent-ridge-test"))
		g.AddNode(&model.Node{ID: a, Name: a, Type: model.NodePackage})
		g.AddNode(&model.Node{ID: b, Name: b, Type: model.NodePackage})
		g.AddEdge(&model.Edge{Source: a, Target: b, Type: model.EdgeDependency})
		g.AddEdge(&model.Edge{Source: b, Target: a, Type: model.EdgeDependency})
		return g
	}

	v1 := ValidateGraph(makeGraph("pkg:a", "pkg:b"), nil)
	v2 := ValidateGraph(makeGraph("pkg:a", "pkg:b"), nil)
	v3 := ValidateGraph(makeGraph("pkg:c", "pkg:d"), nil)

	subj := func(vs []Violation) string {
		for _, v := range vs {
			if v.Rule == "no_circular_dependencies" {
				return v.Subject
			}
		}
		t.Fatal("no cycle violation found")
		return ""
	}

	if subj(v1) != subj(v2) {
		t.Fatalf("cycle subject not deterministic: %q vs %q", subj(v1), subj(v2))
	}
	if subj(v1) == subj(v3) {
		t.Fatalf("different cycles must not share a subject, both got %q", subj(v1))
	}
}
