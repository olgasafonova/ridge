package golang

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

// writeGoAt writes a Go file at a path below root, creating folders.
func writeGoAt(t *testing.T, root, rel, content string) string {
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

const sameNameServer = `package api

import "net/http"

func Register() {
	http.HandleFunc("/ping", nil)
}
`

// endpointIDs returns the IDs of the endpoint nodes in nodes.
func endpointIDs(nodes []*model.Node) []string {
	var ids []string
	for _, n := range nodes {
		if n.Type == model.NodeEndpoint {
			ids = append(ids, n.ID)
		}
	}
	return ids
}

// Two services each have api/server.go. The bare file name gave both
// endpoints "endpoint:server.go:6".
func TestAnalyzeRooted_SameNamedFilesGetOwnEndpointIDs(t *testing.T) {
	root := t.TempDir()
	a := New()
	for _, svc := range []string{"billing", "orders"} {
		rel := svc + "/api/server.go"
		nodes, _, err := a.AnalyzeRooted(root, writeGoAt(t, root, rel, sameNameServer))
		if err != nil {
			t.Fatalf("AnalyzeRooted %s: %v", rel, err)
		}
		ids := endpointIDs(nodes)
		if len(ids) != 1 || ids[0] != "endpoint:"+rel+":6" {
			t.Fatalf("%s: endpoint IDs %v, want [endpoint:%s:6]", rel, ids, rel)
		}
	}
}

func TestAnalyzeRooted_FallsBackOutsideRoot(t *testing.T) {
	path := writeGoAt(t, t.TempDir(), "api/server.go", sameNameServer)
	a := New()

	for _, root := range []string{"", t.TempDir()} {
		nodes, _, err := a.AnalyzeRooted(root, path)
		if err != nil {
			t.Fatalf("AnalyzeRooted: %v", err)
		}
		if ids := endpointIDs(nodes); len(ids) != 1 || ids[0] != "endpoint:server.go:6" {
			t.Fatalf("root %q: endpoint IDs %v", root, ids)
		}
	}
}
