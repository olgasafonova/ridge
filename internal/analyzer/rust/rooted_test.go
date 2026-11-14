package rust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

// writeRustAt writes a Rust file at a path below root, creating folders.
func writeRustAt(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

const sameNameActix = `use actix_web::{get, Responder};

#[get("/ping")]
async fn ping() -> impl Responder {
    "ok"
}
`

// assertRootedFile checks one analyzed file: its module ID and name, its
// endpoint ID, and that every edge starts at the module.
func assertRootedFile(t *testing.T, nodes []*model.Node, edges []*model.Edge, wantMod, wantEndpoint string) {
	t.Helper()
	if nodes[0].ID != wantMod || nodes[0].Name != "handlers" {
		t.Fatalf("module: got %q named %q, want %q named handlers", nodes[0].ID, nodes[0].Name, wantMod)
	}
	if findNodeByID(nodes, wantEndpoint) == nil {
		t.Fatalf("endpoint %q missing from %v", wantEndpoint, nodeIDs(nodes))
	}
	for _, e := range edges {
		if e.Source != wantMod {
			t.Fatalf("edge source %q, want %q", e.Source, wantMod)
		}
	}
}

// isSizeStub reports whether nodes is the single size-skipped module stub
// with the given ID.
func isSizeStub(nodes []*model.Node, id string) bool {
	if len(nodes) != 1 {
		return false
	}
	return nodes[0].ID == id && nodes[0].Properties["skipped"] == "size"
}

// Two crates of one workspace each have src/handlers.rs. The parent-folder ID
// gave both "mod:src/handlers" and both endpoints "endpoint:handlers.rs:3".
func TestAnalyzeRooted_SameNamedFilesGetOwnIDs(t *testing.T) {
	root := t.TempDir()
	a := New()
	for _, crate := range []string{"billing", "orders"} {
		rel := crate + "/src/handlers.rs"
		nodes, edges, err := a.AnalyzeRooted(root, writeRustAt(t, root, rel, sameNameActix))
		if err != nil {
			t.Fatalf("AnalyzeRooted %s: %v", rel, err)
		}
		assertRootedFile(t, nodes, edges, "mod:"+crate+"/src/handlers", "endpoint:"+rel+":3")
	}
}

func TestAnalyzeRooted_FallsBackOutsideRoot(t *testing.T) {
	path := writeRustAt(t, t.TempDir(), "src/handlers.rs", sameNameActix)
	a := New()

	for _, root := range []string{"", t.TempDir()} {
		nodes, _, err := a.AnalyzeRooted(root, path)
		if err != nil {
			t.Fatalf("AnalyzeRooted: %v", err)
		}
		if nodes[0].ID != "mod:src/handlers" {
			t.Fatalf("root %q: module ID %q, want mod:src/handlers", root, nodes[0].ID)
		}
		if findNodeByID(nodes, "endpoint:handlers.rs:3") == nil {
			t.Fatalf("root %q: endpoint IDs %v", root, nodeIDs(nodes))
		}
	}
}

func TestAnalyzeRooted_OversizedFileKeepsRootedID(t *testing.T) {
	root := t.TempDir()
	path := writeRustAt(t, root, "gen/huge.rs", strings.Repeat(" ", maxFileBytes+1))

	nodes, _, err := New().AnalyzeRooted(root, path)
	if err != nil {
		t.Fatalf("AnalyzeRooted: %v", err)
	}
	if !isSizeStub(nodes, "mod:gen/huge") {
		t.Fatalf("got %v", nodes)
	}
}
