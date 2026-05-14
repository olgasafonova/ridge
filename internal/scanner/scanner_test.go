package scanner_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/olgasafonova/ridge/internal/analyzer/golang"
	"github.com/olgasafonova/ridge/internal/analyzer/markdown"
	"github.com/olgasafonova/ridge/internal/analyzer/python"
	"github.com/olgasafonova/ridge/internal/analyzer/typescript"
	"github.com/olgasafonova/ridge/internal/scanner"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestScan_GoProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main

import "fmt"

func main() { fmt.Println("hi") }
`)
	writeFile(t, dir, "handler.go", `package main

import "net/http"

func init() {
	http.HandleFunc("/health", nil)
}
`)

	s := scanner.New(testLogger(), golang.New())
	graph, err := s.Scan(dir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if graph.NodeCount() == 0 {
		t.Fatal("expected nodes from scan")
	}
	if graph.EdgeCount() == 0 {
		t.Fatal("expected edges from scan")
	}
}

func TestScan_SkipsNodeModules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main
func main() {}
`)
	// Files in node_modules should be skipped
	writeFile(t, dir, "node_modules/pkg/main.go", `package pkg
import "database/sql"
var _ *sql.DB
`)

	s := scanner.New(testLogger(), golang.New())
	graph, err := s.Scan(dir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Should only have nodes from the root main.go, not from node_modules
	for _, n := range graph.Nodes() {
		if n.Name == "pkg" {
			t.Fatal("node_modules directory should be skipped")
		}
	}
}

// TestScan_SkipsSymlinks is a regression test for the Carlini-scaffold
// finding: WalkDir surfaced symlinks to the analyzers, and analyzer
// os.ReadFile then followed the link. Verified PoC: a symlink under the
// scanned root pointing at /etc/passwd would be parsed as Python.
//
// This test creates a Go file inside the scan root and a symlink under the
// root pointing at a Go file that lives OUTSIDE the root. The symlink target
// has a unique top-level package name so we can detect whether it leaked
// into the graph. Expectation: only the inside file appears in the graph.
func TestScan_SkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "real.go", `package real

func RealFn() {}
`)

	// Out-of-root file with a distinct package name.
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "leaked.go")
	if err := os.WriteFile(outsideFile, []byte(`package leaked

func LeakedFn() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Symlink under root pointing at the outside file.
	link := filepath.Join(root, "innocent.go")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skipf("symlink unsupported on this filesystem: %v", err)
	}

	s := scanner.New(testLogger(), golang.New())
	graph, err := s.Scan(root)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	for _, node := range graph.Nodes() {
		if node.Name == "leaked" {
			t.Fatalf("scanner followed symlink into outside package: %+v", node)
		}
	}
}

func TestScan_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	s := scanner.New(testLogger(), golang.New())
	graph, err := s.Scan(dir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if graph.NodeCount() != 0 {
		t.Fatalf("expected 0 nodes in empty dir, got %d", graph.NodeCount())
	}
}

func TestScan_UnsupportedFileSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "readme.md", `# Hello`)
	writeFile(t, dir, "data.json", `{"key":"value"}`)

	s := scanner.New(testLogger(), golang.New())
	graph, err := s.Scan(dir)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if graph.NodeCount() != 0 {
		t.Fatal("non-Go files should produce no nodes")
	}
}

func TestSupportedExtensions(t *testing.T) {
	s := scanner.New(testLogger(), golang.New())
	exts := s.SupportedExtensions()
	if len(exts) != 1 || exts[0] != ".go" {
		t.Fatalf("expected [.go], got %v", exts)
	}
}

func TestScanWithOptions_MaxFiles(t *testing.T) {
	dir := t.TempDir()
	// Create 20 Go files, each producing at least one node
	for i := 0; i < 20; i++ {
		writeFile(t, dir, fmt.Sprintf("pkg%d/main.go", i), fmt.Sprintf(`package pkg%d
import "fmt"
var _ = fmt.Sprintf
`, i))
	}

	s := scanner.New(testLogger(), golang.New())
	result, err := s.ScanWithOptions(context.Background(), dir, scanner.ScanOptions{MaxFiles: 5})

	if !errors.Is(err, scanner.ErrLimitReached) {
		t.Fatalf("expected ErrLimitReached, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected partial result, got nil")
	}
	if !result.Truncated {
		t.Fatal("expected Truncated=true")
	}
	if result.Stats.FilesAnalyzed > 5 {
		t.Fatalf("expected <= 5 files analyzed, got %d", result.Stats.FilesAnalyzed)
	}
}

func TestScanWithOptions_MaxNodes(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		writeFile(t, dir, fmt.Sprintf("pkg%d/main.go", i), fmt.Sprintf(`package pkg%d
import "fmt"
var _ = fmt.Sprintf
`, i))
	}

	s := scanner.New(testLogger(), golang.New())
	result, err := s.ScanWithOptions(context.Background(), dir, scanner.ScanOptions{MaxNodes: 3})

	if !errors.Is(err, scanner.ErrLimitReached) {
		t.Fatalf("expected ErrLimitReached, got: %v", err)
	}
	if result.Stats.NodesFound > 3 {
		t.Fatalf("expected <= 3 nodes, got %d", result.Stats.NodesFound)
	}
	if !result.Truncated {
		t.Fatal("expected Truncated=true")
	}
}

func TestScanWithOptions_Timeout(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main
func main() {}
`)

	s := scanner.New(testLogger(), golang.New())

	// Use an already-cancelled context to guarantee timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(1 * time.Millisecond) // ensure timeout fires

	result, err := s.ScanWithOptions(ctx, dir, scanner.DefaultScanOptions())
	if !errors.Is(err, scanner.ErrLimitReached) {
		t.Fatalf("expected ErrLimitReached from timeout, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected partial result")
	}
	if !result.Truncated {
		t.Fatal("expected Truncated=true from timeout")
	}
}

func TestScanWithOptions_ExtraSkipDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main
func main() {}
`)
	writeFile(t, dir, "custom_skip/hidden.go", `package hidden
import "database/sql"
var _ *sql.DB
`)

	s := scanner.New(testLogger(), golang.New())
	result, err := s.ScanWithOptions(context.Background(), dir, scanner.ScanOptions{
		SkipDirs: []string{"custom_skip"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, n := range result.Graph.Nodes() {
		if n.Name == "hidden" {
			t.Fatal("custom_skip directory should have been skipped")
		}
	}
}

func TestScanWithOptions_SkipGlobs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main
import "fmt"
var _ = fmt.Sprintf
`)
	writeFile(t, dir, "main_test.go", `package main
import "database/sql"
var _ *sql.DB
`)

	s := scanner.New(testLogger(), golang.New())
	result, err := s.ScanWithOptions(context.Background(), dir, scanner.ScanOptions{
		SkipGlobs: []string{"*_test.go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Stats.FilesSkipped != 1 {
		t.Fatalf("expected 1 file skipped, got %d", result.Stats.FilesSkipped)
	}
	if result.Stats.FilesAnalyzed != 1 {
		t.Fatalf("expected 1 file analyzed, got %d", result.Stats.FilesAnalyzed)
	}
}

func TestScan_BackwardsCompatible(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main
import "fmt"
func main() { fmt.Println("hi") }
`)

	s := scanner.New(testLogger(), golang.New())
	graph, err := s.Scan(dir)
	if err != nil {
		t.Fatalf("Scan() wrapper failed: %v", err)
	}
	if graph == nil {
		t.Fatal("expected non-nil graph from Scan()")
	}
	if graph.NodeCount() == 0 {
		t.Fatal("expected nodes from Scan()")
	}
}

func TestScanWithOptions_ParallelWorkers(t *testing.T) {
	dir := t.TempDir()
	// Create 50 Go files, each with a unique package and import
	for i := 0; i < 50; i++ {
		writeFile(t, dir, fmt.Sprintf("pkg%d/main.go", i), fmt.Sprintf(`package pkg%d
import "fmt"
var _ = fmt.Sprintf
`, i))
	}

	s := scanner.New(testLogger(), golang.New())
	result, err := s.ScanWithOptions(context.Background(), dir, scanner.ScanOptions{Workers: 4})
	if err != nil {
		t.Fatalf("parallel scan failed: %v", err)
	}

	if result.Stats.FilesAnalyzed != 50 {
		t.Fatalf("expected 50 files analyzed, got %d", result.Stats.FilesAnalyzed)
	}
	if result.Graph.NodeCount() < 50 {
		t.Fatalf("expected at least 50 nodes, got %d", result.Graph.NodeCount())
	}
}

func TestScanWithOptions_ParallelCancellation(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		writeFile(t, dir, fmt.Sprintf("pkg%d/main.go", i), fmt.Sprintf(`package pkg%d
import "fmt"
var _ = fmt.Sprintf
`, i))
	}

	s := scanner.New(testLogger(), golang.New())

	// Already-expired context
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(1 * time.Millisecond)

	result, err := s.ScanWithOptions(ctx, dir, scanner.ScanOptions{Workers: 4})
	if !errors.Is(err, scanner.ErrLimitReached) {
		t.Fatalf("expected ErrLimitReached, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected partial result")
	}
	if !result.Truncated {
		t.Fatal("expected Truncated=true")
	}
}

func TestAnalyzerClone_Go(t *testing.T) {
	original := golang.New()
	cloned := original.Clone()

	if cloned == nil {
		t.Fatal("Clone() returned nil")
	}

	// Verify the clone works by analyzing a file
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main
import "fmt"
func main() { fmt.Println("hi") }
`)

	nodes, edges, err := cloned.Analyze(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("cloned analyzer failed: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("cloned analyzer produced no nodes")
	}
	if len(edges) == 0 {
		t.Fatal("cloned analyzer produced no edges")
	}
}

func TestAnalyzerSignatures_AreUniqueAndStable(t *testing.T) {
	// Every analyzer must return a non-empty signature, and the signatures
	// must be distinct so a FileEntry produced by one analyzer can never
	// silently satisfy the cache check of another. Clone() must also return
	// an analyzer with the same signature (cloning is for goroutine safety,
	// not for changing behavior).
	analyzers := []scanner.Analyzer{
		golang.New(),
		typescript.New(),
		python.New(),
		markdown.New(),
	}

	seen := make(map[string]string)
	for _, a := range analyzers {
		sig := a.Signature()
		if sig == "" {
			t.Errorf("analyzer %q has empty signature", a.Language())
			continue
		}
		if prev, ok := seen[sig]; ok {
			t.Errorf("signature collision: %q used by both %q and %q", sig, prev, a.Language())
		}
		seen[sig] = a.Language()

		if cloneSig := a.Clone().Signature(); cloneSig != sig {
			t.Errorf("%s: Clone().Signature()=%q, want %q", a.Language(), cloneSig, sig)
		}
	}
}
