package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olgasafonova/ridge/internal/detector"
)

// writeFixture creates a file under dir, making parent directories.
func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cycleRepo builds a Go module with a genuine import cycle between two
// packages, which arch_validate reports as no_circular_dependencies.
func cycleRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeFixture(t, repo, "go.mod", "module example.com/app\n\ngo 1.22\n")
	writeFixture(t, repo, "alpha/alpha.go", `package alpha

import "example.com/app/beta"

var _ = beta.B
`)
	writeFixture(t, repo, "beta/beta.go", `package beta

import "example.com/app/alpha"

var B = 1
var _ = alpha.A
`)
	writeFixture(t, repo, "alpha/exported.go", "package alpha\n\nvar A = 1\n")
	return repo
}

func TestArchValidate_BaselineWriteThenCheck(t *testing.T) {
	repo := cycleRepo(t)
	h := NewHandlerRegistry(testLogger())
	ctx := context.Background()

	// Plain validation sees the cycle.
	plain, err := h.archValidate(ctx, ArchValidateArgs{Path: repo})
	if err != nil {
		t.Fatalf("plain validate: %v", err)
	}
	if plain.Valid || len(plain.Violations) == 0 {
		t.Fatalf("expected cycle violations in fixture repo, got valid=%v violations=%v", plain.Valid, plain.Violations)
	}

	// Write the baseline: current violations are grandfathered.
	written, err := h.archValidate(ctx, ArchValidateArgs{Path: repo, Baseline: "write"})
	if err != nil {
		t.Fatalf("baseline write: %v", err)
	}
	wantFile := filepath.Join(repo, detector.BaselineFileName)
	if written.BaselineFile != wantFile {
		t.Fatalf("expected baseline at %s, got %s", wantFile, written.BaselineFile)
	}
	if _, err := os.Stat(wantFile); err != nil {
		t.Fatalf("baseline file not written: %v", err)
	}

	// Check mode: same violations, but the ratchet grandfathers them.
	checked, err := h.archValidate(ctx, ArchValidateArgs{Path: repo, Baseline: "check"})
	if err != nil {
		t.Fatalf("baseline check: %v", err)
	}
	if !checked.Valid {
		t.Fatalf("expected valid=true when all violations are grandfathered, got %+v", checked)
	}
	if checked.Grandfathered == 0 {
		t.Fatalf("expected grandfathered > 0, got %+v", checked)
	}
	if len(checked.NewViolations) != 0 {
		t.Fatalf("expected no new violations, got %v", checked.NewViolations)
	}
	// Visibility invariant: the full list stays reported even when valid.
	if len(checked.Violations) == 0 {
		t.Fatal("grandfathered violations must remain visible in violations")
	}
}

func TestArchValidate_BaselineCheckCatchesNewViolation(t *testing.T) {
	repo := cycleRepo(t)
	h := NewHandlerRegistry(testLogger())
	ctx := context.Background()

	if _, err := h.archValidate(ctx, ArchValidateArgs{Path: repo, Baseline: "write"}); err != nil {
		t.Fatalf("baseline write: %v", err)
	}

	// Introduce a NEW violation after the baseline: a second, distinct cycle.
	writeFixture(t, repo, "gamma/gamma.go", `package gamma

import "example.com/app/delta"

var _ = delta.D
`)
	writeFixture(t, repo, "delta/delta.go", `package delta

import "example.com/app/gamma"

var D = 1
`)

	// Fresh registry: the scan cache (5-min TTL) would otherwise serve the
	// pre-change graph. Equivalent to a new MCP session after the change.
	h = NewHandlerRegistry(testLogger())
	checked, err := h.archValidate(ctx, ArchValidateArgs{Path: repo, Baseline: "check"})
	if err != nil {
		t.Fatalf("baseline check: %v", err)
	}
	if checked.Valid {
		t.Fatalf("expected valid=false with a new cycle, got %+v", checked)
	}
	if len(checked.NewViolations) == 0 {
		t.Fatal("expected the new cycle in new_violations")
	}
	if checked.Grandfathered == 0 {
		t.Fatalf("expected original violations to stay grandfathered, got %+v", checked)
	}
}

func TestArchValidate_BaselineCheckWithoutFile(t *testing.T) {
	repo := cycleRepo(t)
	h := NewHandlerRegistry(testLogger())

	_, err := h.archValidate(context.Background(), ArchValidateArgs{Path: repo, Baseline: "check"})
	if err == nil {
		t.Fatal("expected error when no baseline file exists")
	}
	if !strings.Contains(err.Error(), "baseline=\"write\"") {
		t.Fatalf("error should tell the caller how to create a baseline, got: %v", err)
	}
}

func TestArchValidate_BaselineInvalidMode(t *testing.T) {
	repo := cycleRepo(t)
	h := NewHandlerRegistry(testLogger())

	_, err := h.archValidate(context.Background(), ArchValidateArgs{Path: repo, Baseline: "bogus"})
	if err == nil {
		t.Fatal("expected error for invalid baseline mode")
	}
}

func TestArchValidate_BaselineFileEscapesRepo(t *testing.T) {
	repo := cycleRepo(t)
	h := NewHandlerRegistry(testLogger())
	outside := filepath.Join(t.TempDir(), "stolen.json")

	_, err := h.archValidate(context.Background(), ArchValidateArgs{Path: repo, Baseline: "write", BaselineFile: outside})
	if err == nil {
		t.Fatal("expected error when baseline_file escapes the scanned repo")
	}
}
