package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olgasafonova/ridge/internal/drift"
)

// initDriftTestRepo creates a temp git repo holding a minimal Go module across
// two commits, and returns the repo path plus the sha of the first commit. The
// second commit adds a package, so a diff between the two has something to
// report.
func initDriftTestRepo(t *testing.T) (repoPath, firstCommit string) {
	t.Helper()
	dir := t.TempDir()

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s: %v", args, string(out), err)
		}
	}

	git("init")
	git("checkout", "-b", "main")

	writeTestFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.22\n")
	writeTestFile(t, dir, "cmd/api/main.go", "package main\n\nfunc main() {}\n")
	git("add", ".")
	git("commit", "-m", "first commit")

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse failed: %v", err)
	}
	firstCommit = strings.TrimSpace(string(out))

	writeTestFile(t, dir, "internal/store/store.go", "package store\n\ntype Store struct{}\n")
	git("add", ".")
	git("commit", "-m", "second commit")

	return dir, firstCommit
}

// writeDriftSnapshot saves a baseline snapshot inside repoPath and returns its
// path. It has to live inside the repo because archDiff runs the snapshot file
// through ValidateOutputPath against the scan root.
func writeDriftSnapshot(t *testing.T, repoPath string) string {
	t.Helper()
	snapshotFile := filepath.Join(repoPath, "baseline.json")
	if _, err := drift.Save(renderFixtureGraph(), snapshotFile, "baseline"); err != nil {
		t.Fatalf("drift.Save failed: %v", err)
	}
	return snapshotFile
}

// TestArchDiff_RejectsBadInput covers every guard archDiff runs before it
// loads a snapshot or scans anything.
func TestArchDiff_RejectsBadInput(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "go.mod", "module example.com/app\n\ngo 1.22\n")

	tests := []struct {
		name    string
		args    ArchDiffArgs
		wantErr string
	}{
		{"neither path nor repo", ArchDiffArgs{SnapshotFile: "snap.json"}, "either path or repo is required"},
		{"path does not exist", ArchDiffArgs{Path: "/nonexistent/ridge/fixture", SnapshotFile: "snap.json"}, "path does not exist"},
		{"snapshot file missing from args", ArchDiffArgs{Path: repo}, "snapshot_file is required"},
		{
			name:    "snapshot file escapes the scan root",
			args:    ArchDiffArgs{Path: repo, SnapshotFile: filepath.Join(repo, "..", "outside.json")},
			wantErr: "invalid snapshot file path",
		},
		{
			name:    "snapshot file does not exist on disk",
			args:    ArchDiffArgs{Path: repo, SnapshotFile: filepath.Join(repo, "absent.json")},
			wantErr: "loading snapshot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandlerRegistry(testLogger())
			_, err := h.archDiff(context.Background(), tt.args)
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q; want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestArchDiff_ComparesSnapshotAgainstCurrent is the happy path: a baseline
// snapshot of a different shape must produce changes, with the refs labelled so
// the caller can tell which side is which.
func TestArchDiff_ComparesSnapshotAgainstCurrent(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, "go.mod", "module example.com/app\n\ngo 1.22\n")
	writeTestFile(t, repo, "cmd/api/main.go", "package main\n\nfunc main() {}\n")
	snapshotFile := writeDriftSnapshot(t, repo)

	h := NewHandlerRegistry(testLogger())
	report, err := h.archDiff(context.Background(), ArchDiffArgs{Path: repo, SnapshotFile: snapshotFile})
	if err != nil {
		t.Fatalf("archDiff failed: %v", err)
	}

	if report.BaseRef != snapshotFile {
		t.Errorf("BaseRef: want the snapshot path %q, got %q", snapshotFile, report.BaseRef)
	}
	if report.CompareRef != "current" {
		t.Errorf("CompareRef: want current, got %q", report.CompareRef)
	}
	if !report.HasChanges() {
		t.Error("the snapshot describes a different architecture, so changes were expected")
	}
	if report.Summary == "" {
		t.Error("expected a summary on the diff report")
	}
}

// TestArchDrift_RejectsBadInput covers the argument guards and the checkout
// failure path, where the error has to say which ref was at fault.
func TestArchDrift_RejectsBadInput(t *testing.T) {
	repo, _ := initDriftTestRepo(t)

	tests := []struct {
		name    string
		args    ArchDriftArgs
		wantErr string
	}{
		{"neither path nor repo", ArchDriftArgs{BaseRef: "HEAD"}, "either path or repo is required"},
		{"path does not exist", ArchDriftArgs{Path: "/nonexistent/ridge/fixture", BaseRef: "HEAD"}, "path does not exist"},
		{"base ref missing", ArchDriftArgs{Path: repo}, "base_ref is required"},
		{"base ref does not resolve", ArchDriftArgs{Path: repo, BaseRef: "no-such-ref"}, "checking out base ref"},
		{"head ref does not resolve", ArchDriftArgs{Path: repo, BaseRef: "HEAD", HeadRef: "no-such-ref"}, "checking out head ref"},
		{"base ref is shell-unsafe", ArchDriftArgs{Path: repo, BaseRef: "HEAD;rm -rf /"}, "invalid ref"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandlerRegistry(testLogger())
			_, err := h.archDrift(context.Background(), tt.args)
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q; want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestArchDrift_ComparesTwoRefs walks the two-worktree path and checks that the
// package added in the second commit shows up as drift, with head_ref
// defaulting to HEAD.
func TestArchDrift_ComparesTwoRefs(t *testing.T) {
	repo, firstCommit := initDriftTestRepo(t)

	h := NewHandlerRegistry(testLogger())
	report, err := h.archDrift(context.Background(), ArchDriftArgs{Path: repo, BaseRef: firstCommit})
	if err != nil {
		t.Fatalf("archDrift failed: %v", err)
	}

	if report.BaseRef != firstCommit {
		t.Errorf("BaseRef: want %q, got %q", firstCommit, report.BaseRef)
	}
	if report.CompareRef != "HEAD" {
		t.Errorf("CompareRef: want HEAD when head_ref is omitted, got %q", report.CompareRef)
	}
	if !report.HasChanges() {
		t.Error("the second commit adds a package, so drift was expected")
	}
}

// TestArchDrift_SameRefHasNoChanges is the control for the test above: diffing
// a ref against itself must come back clean, which is what makes a non-empty
// diff meaningful.
func TestArchDrift_SameRefHasNoChanges(t *testing.T) {
	repo, _ := initDriftTestRepo(t)

	h := NewHandlerRegistry(testLogger())
	report, err := h.archDrift(context.Background(), ArchDriftArgs{Path: repo, BaseRef: "HEAD", HeadRef: "HEAD"})
	if err != nil {
		t.Fatalf("archDrift failed: %v", err)
	}
	if report.HasChanges() {
		t.Errorf("HEAD against itself should be unchanged, got %d changes", len(report.Changes))
	}
}

// TestCheckoutAndScan covers the shared worktree helper directly: the happy
// path returns a scanned graph plus a cleanup that removes the worktree, and
// the failure path names the role so the caller knows which argument to fix.
func TestCheckoutAndScan(t *testing.T) {
	repo, firstCommit := initDriftTestRepo(t)
	h := NewHandlerRegistry(testLogger())

	t.Run("scans the checked-out ref", func(t *testing.T) {
		graph, cleanup, err := h.checkoutAndScan(context.Background(), repo, firstCommit, "base")
		if err != nil {
			t.Fatalf("checkoutAndScan failed: %v", err)
		}
		defer cleanup()

		if graph == nil {
			t.Fatal("expected a graph from the scanned worktree")
		}
		if graph.NodeCount() == 0 {
			t.Error("the first commit holds a Go module, so the graph should have nodes")
		}
	})

	t.Run("names the role in the checkout error", func(t *testing.T) {
		_, cleanup, err := h.checkoutAndScan(context.Background(), repo, "no-such-ref", "head")
		if err == nil {
			cleanup()
			t.Fatal("expected an error for an unresolvable ref")
		}
		if !strings.Contains(err.Error(), "checking out head ref") {
			t.Errorf("error = %q; want it to name the head role", err.Error())
		}
	})
}

// TestArchDriftExplain_AddsNarrative checks the wrapper returns both halves:
// the structured report and the templated narrative built from it.
func TestArchDriftExplain_AddsNarrative(t *testing.T) {
	repo, firstCommit := initDriftTestRepo(t)

	h := NewHandlerRegistry(testLogger())
	res, err := h.archDriftExplain(context.Background(), ArchDriftExplainArgs{Path: repo, BaseRef: firstCommit})
	if err != nil {
		t.Fatalf("archDriftExplain failed: %v", err)
	}

	if res.Report == nil {
		t.Fatal("expected the underlying diff report to be carried through")
	}
	if res.Report.CompareRef != "HEAD" {
		t.Errorf("CompareRef: want HEAD, got %q", res.Report.CompareRef)
	}
	if res.Narrative == "" {
		t.Fatal("expected a narrative alongside the report")
	}
	if !strings.Contains(res.Narrative, firstCommit) {
		t.Errorf("narrative should name the base ref, got %q", res.Narrative)
	}
}

// TestArchDriftExplain_PropagatesDriftError verifies the wrapper fails on the
// same conditions archDrift does rather than returning an empty narrative.
func TestArchDriftExplain_PropagatesDriftError(t *testing.T) {
	h := NewHandlerRegistry(testLogger())
	_, err := h.archDriftExplain(context.Background(), ArchDriftExplainArgs{Path: t.TempDir()})
	if err == nil {
		t.Fatal("expected the missing base_ref error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "base_ref is required") {
		t.Errorf("error = %q; want it to contain %q", err.Error(), "base_ref is required")
	}
}
