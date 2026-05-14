package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func TestPopulateSamples_FileBackedNode(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "service.go")
	content := "package svc\n\n// helper does something useful.\nfunc helper() string {\n\treturn \"ok\"\n}\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	g := model.NewGraph(dir)
	g.AddNode(&model.Node{ID: "endpoint:service.go:4", Path: file, Type: model.NodeEndpoint, Properties: map[string]string{"line": "4"}})

	PopulateSamples(g, 3)

	got := g.GetNode("endpoint:service.go:4").Source
	if !strings.HasPrefix(got, "func helper() string {") {
		t.Fatalf("snippet should start at line 4 (func helper...), got: %q", got)
	}
	if strings.Count(got, "\n") > 3 {
		t.Fatalf("snippet should be at most 3 lines, got: %q", got)
	}
}

func TestPopulateSamples_DirectoryPathSkipped(t *testing.T) {
	dir := t.TempDir()
	g := model.NewGraph(dir)
	g.AddNode(&model.Node{ID: "pkg:svc", Path: dir, Type: model.NodePackage})

	PopulateSamples(g, 0) // 0 → default

	if got := g.GetNode("pkg:svc").Source; got != "" {
		t.Fatalf("directory-path node should be skipped, got Source=%q", got)
	}
}

func TestPopulateSamples_StartAtFirstNonBlank(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "note.md")
	content := "\n\n\n# Title\nbody line one\nbody line two\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	g := model.NewGraph(dir)
	g.AddNode(&model.Node{ID: "note:note.md", Path: file, Type: model.NodeNote})

	PopulateSamples(g, 2)

	got := g.GetNode("note:note.md").Source
	if !strings.HasPrefix(got, "# Title") {
		t.Fatalf("snippet should skip leading blanks and start at '# Title', got: %q", got)
	}
}

func TestPopulateSamples_PreservesExistingSource(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte("line1\nline2\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	g := model.NewGraph(dir)
	g.AddNode(&model.Node{ID: "n1", Path: file, Source: "preset"})

	PopulateSamples(g, 5)

	if got := g.GetNode("n1").Source; got != "preset" {
		t.Fatalf("existing Source should be preserved, got: %q", got)
	}
}

func TestPopulateSamples_MissingFileTolerated(t *testing.T) {
	dir := t.TempDir()
	g := model.NewGraph(dir)
	g.AddNode(&model.Node{ID: "missing", Path: filepath.Join(dir, "nope.go")})

	PopulateSamples(g, 3) // must not panic

	if got := g.GetNode("missing").Source; got != "" {
		t.Fatalf("missing file should leave Source empty, got: %q", got)
	}
}
