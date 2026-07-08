package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestFile creates a file (and parent dirs) under dir.
func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestArchBoundaries_VerifiabilityFields checks that the topology verdict
// carries its evidence: marker signals with a deployable-unit count, a reason,
// and scan-derived graph signals.
func TestArchBoundaries_VerifiabilityFields(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.22\n")
	writeTestFile(t, dir, "cmd/api/main.go", "package main\n\nfunc main() {}\n")
	writeTestFile(t, dir, "cmd/worker/main.go", "package main\n\nfunc main() {}\n")

	h := NewHandlerRegistry(testLogger())
	res, err := h.archBoundaries(context.Background(), ArchBoundariesArgs{Path: dir})
	if err != nil {
		t.Fatalf("archBoundaries failed: %v", err)
	}

	if res.Reason == "" {
		t.Error("expected a non-empty reason behind the topology verdict")
	}
	if res.Signals.DeployableUnits != 2 {
		t.Errorf("Signals.DeployableUnits: want 2 (cmd/api, cmd/worker), got %d", res.Signals.DeployableUnits)
	}
	if res.GraphSignals == nil {
		t.Fatal("expected graph_signals from the best-effort scan, got nil")
	}
	if res.GraphSignals.SharedDatabase {
		t.Error("no database in fixture; SharedDatabase should be false")
	}
	if !strings.Contains(res.Summary, "deployable units") {
		t.Errorf("summary should surface deployable units, got %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "cross-boundary edges") {
		t.Errorf("summary should surface cross-boundary edge count, got %q", res.Summary)
	}
}
