package python

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

// writePyAt writes a Python file at a path below root, creating folders.
func writePyAt(t *testing.T, root, rel, content string) string {
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

const sameNameFlask = `from flask import Flask

app = Flask(__name__)

@app.route('/ping')
def ping():
    return ''
`

const sameNameDjangoURLs = `from django.urls import path

urlpatterns = [
    path('ping/', views.ping),
]
`

// endpointsOf returns the endpoint nodes in nodes.
func endpointsOf(nodes []*model.Node) []*model.Node {
	var eps []*model.Node
	for _, n := range nodes {
		if n.Type == model.NodeEndpoint {
			eps = append(eps, n)
		}
	}
	return eps
}

// assertRootedIDs checks the module node and every endpoint of one file carry
// IDs built from its root-relative path, and every edge starts at the module.
func assertRootedIDs(t *testing.T, nodes []*model.Node, edges []*model.Edge, rel string) {
	t.Helper()
	wantMod := "mod:" + strings.TrimSuffix(rel, ".py")
	if nodes[0].ID != wantMod {
		t.Fatalf("module ID %q, want %q", nodes[0].ID, wantMod)
	}
	assertEndpointPrefix(t, nodes, "endpoint:"+rel+":")
	assertEdgesFrom(t, edges, wantMod)
}

// assertEndpointPrefix checks nodes hold at least one endpoint and every
// endpoint ID starts with prefix.
func assertEndpointPrefix(t *testing.T, nodes []*model.Node, prefix string) {
	t.Helper()
	eps := endpointsOf(nodes)
	if len(eps) == 0 {
		t.Fatalf("no endpoint in %v", nodes)
	}
	for _, ep := range eps {
		if !strings.HasPrefix(ep.ID, prefix) {
			t.Fatalf("endpoint ID %q, want prefix %q", ep.ID, prefix)
		}
	}
}

// assertEdgesFrom checks every edge starts at source.
func assertEdgesFrom(t *testing.T, edges []*model.Edge, source string) {
	t.Helper()
	for _, e := range edges {
		if e.Source != source {
			t.Fatalf("edge source %q, want %q", e.Source, source)
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

// Two services each have app/routes.py and app/urls.py. The parent-folder ID
// gave both "mod:app/routes" and both endpoints "endpoint:routes.py:<line>".
func TestAnalyzeRooted_SameNamedFilesGetOwnIDs(t *testing.T) {
	root := t.TempDir()
	a := New()
	for _, svc := range []string{"billing", "orders"} {
		for name, src := range map[string]string{"routes.py": sameNameFlask, "urls.py": sameNameDjangoURLs} {
			rel := svc + "/app/" + name
			nodes, edges, err := a.AnalyzeRooted(root, writePyAt(t, root, rel, src))
			if err != nil {
				t.Fatalf("AnalyzeRooted %s: %v", rel, err)
			}
			if nodes[0].Name != strings.TrimSuffix(name, ".py") {
				t.Fatalf("module name %q", nodes[0].Name)
			}
			assertRootedIDs(t, nodes, edges, rel)
		}
	}
}

func TestAnalyzeRooted_FallsBackOutsideRoot(t *testing.T) {
	path := writePyAt(t, t.TempDir(), "app/routes.py", sameNameFlask)
	a := New()

	for _, root := range []string{"", t.TempDir()} {
		nodes, _, err := a.AnalyzeRooted(root, path)
		if err != nil {
			t.Fatalf("AnalyzeRooted: %v", err)
		}
		if nodes[0].ID != "mod:app/routes" {
			t.Fatalf("root %q: module ID %q, want mod:app/routes", root, nodes[0].ID)
		}
		assertEndpointPrefix(t, nodes, "endpoint:routes.py:")
	}
}

func TestAnalyzeRooted_OversizedFileKeepsRootedID(t *testing.T) {
	root := t.TempDir()
	path := writePyAt(t, root, "gen/huge.py", strings.Repeat(" ", maxFileBytes+1))

	nodes, _, err := New().AnalyzeRooted(root, path)
	if err != nil {
		t.Fatalf("AnalyzeRooted: %v", err)
	}
	if !isSizeStub(nodes, "mod:gen/huge") {
		t.Fatalf("got %v", nodes)
	}
}
