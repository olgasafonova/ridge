package render

import (
	"sort"
	"strings"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

// groupFixture is a small SvelteKit-shaped graph rooted at /repo.
func groupFixture() *model.ArchGraph {
	g := model.NewGraph("/repo")
	mod := func(id, dir string) {
		g.AddNode(&model.Node{ID: id, Name: id, Type: model.NodeModule, Path: dir})
	}
	mod("mod:src/routes/+page.svelte", "/repo/src/routes")
	mod("mod:src/routes/api/trace/+server", "/repo/src/routes/api/trace")
	mod("mod:src/lib/server/trace", "/repo/src/lib/server")
	mod("mod:src/lib/server/input", "/repo/src/lib/server")
	mod("mod:src/lib/schema", "/repo/src/lib")
	mod("mod:vite.config", "/repo")
	g.AddNode(&model.Node{
		ID: "endpoint:src/routes/api/trace/+server.ts:18", Name: "POST /api/trace", Type: model.NodeEndpoint,
		Path:       "/repo/src/routes/api/trace/+server.ts",
		Properties: map[string]string{"method": "post", "route": "/api/trace"},
	})
	g.AddNode(&model.Node{ID: "infra:database", Name: "Database", Type: model.NodeDatabase})

	dep := func(from, to string) {
		g.AddEdge(&model.Edge{Source: from, Target: to, Type: model.EdgeDependency, Label: to})
	}
	dep("mod:src/routes/api/trace/+server", "mod:src/lib/server/trace")
	dep("mod:src/routes/api/trace/+server", "mod:src/lib/server/input")
	dep("mod:src/lib/server/trace", "mod:src/lib/server/input") // inside one folder
	dep("mod:src/lib/server/trace", "mod:src/lib/schema")
	g.AddEdge(&model.Edge{Source: "mod:src/routes/api/trace/+server", Target: "endpoint:src/routes/api/trace/+server.ts:18", Type: model.EdgeAPICall, Label: "serves"})
	g.AddEdge(&model.Edge{Source: "mod:src/routes/+page.svelte", Target: "route:POST /api/trace", Type: model.EdgeAPICall, Label: "POST /api/trace"})
	g.AddEdge(&model.Edge{Source: "mod:src/lib/server/trace", Target: "infra:database", Type: model.EdgeReadWrite, Label: "pg"})
	return g
}

func edgeLabels(g *model.ArchGraph) map[string]string {
	out := make(map[string]string)
	for _, e := range g.Edges() {
		out[e.Source+" -> "+e.Target] = e.Label
	}
	return out
}

func TestGroupByFolder(t *testing.T) {
	grouped := GroupByFolder(groupFixture(), 3)

	wantNodes := map[string]string{
		"dir:(root)":             "(root) (1)",
		"dir:src/routes":         "src/routes (1)",
		"dir:src/routes/api":     "src/routes/api (2)", // module and its endpoint, cut from src/routes/api/trace
		"dir:src/lib/server":     "src/lib/server (2)",
		"dir:src/lib":            "src/lib (1)",
		"infra:database":         "Database",
		"mod:src/lib/schema":     "", // absent: folded into its folder
		"endpoint:anything else": "",
	}
	for id, name := range wantNodes {
		n := grouped.GetNode(id)
		switch {
		case name == "" && n != nil:
			t.Errorf("node %q should have been folded away", id)
		case name != "" && n == nil:
			t.Errorf("missing node %q", id)
		case name != "" && n.Name != name:
			t.Errorf("node %q name = %q, want %q", id, n.Name, name)
		}
	}
	if got := grouped.NodeCount(); got != 6 {
		t.Errorf("node count = %d, want 6", got)
	}

	wantEdges := map[string]string{
		"dir:src/routes/api -> dir:src/lib/server": "2 imports",
		"dir:src/lib/server -> dir:src/lib":        "1 import",
		"dir:src/routes -> dir:src/routes/api":     "1 call", // fetch resolved to the endpoint, then to its folder
		"dir:src/lib/server -> infra:database":     "1 read write",
	}
	got := edgeLabels(grouped)
	if len(got) != len(wantEdges) {
		t.Errorf("edges = %v, want %v", got, wantEdges)
	}
	for k, label := range wantEdges {
		if got[k] != label {
			t.Errorf("edge %s label = %q, want %q", k, got[k], label)
		}
	}
}

func TestGroupByFolder_Depth(t *testing.T) {
	tests := []struct {
		depth int
		want  []string
	}{
		{1, []string{"dir:(root)", "dir:src"}},
		{2, []string{"dir:(root)", "dir:src/lib", "dir:src/routes"}},
	}
	for _, tt := range tests {
		grouped := GroupByFolder(groupFixture(), tt.depth)
		var got []string
		for _, n := range grouped.Nodes() {
			if isFolderID(n.ID) {
				got = append(got, n.ID)
			}
		}
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("depth %d folders = %v, want %v", tt.depth, got, tt.want)
		}
	}
}

func TestGroupByFolder_DisabledReturnsSameGraph(t *testing.T) {
	g := groupFixture()
	if GroupByFolder(g, 0) != g {
		t.Fatal("depth 0 should return the graph unchanged")
	}
}

// A->B->C plus A->C: the direct arrow carries its own count and must survive
// the transitive reduction the Mermaid renderer applies to file-level graphs.
func TestMermaid_GroupedKeepsCountedArrows(t *testing.T) {
	g := model.NewGraph("/repo")
	for _, dir := range []string{"a", "b", "c"} {
		g.AddNode(&model.Node{ID: "mod:" + dir + "/x", Name: "x", Type: model.NodeModule, Path: "/repo/" + dir})
	}
	for _, pair := range [][2]string{{"a", "b"}, {"b", "c"}, {"a", "c"}} {
		g.AddEdge(&model.Edge{Source: "mod:" + pair[0] + "/x", Target: "mod:" + pair[1] + "/x", Type: model.EdgeDependency})
	}
	opts := DefaultOptions()
	opts.GroupDepth = 1
	out := Mermaid(GroupByFolder(g, opts.GroupDepth), opts)
	if got := strings.Count(out, "1 import"); got != 3 {
		t.Fatalf("expected 3 counted arrows, got %d:\n%s", got, out)
	}
}

func TestGroupByFolder_EdgesBetweenUngroupedNodes(t *testing.T) {
	g := model.NewGraph("/repo")
	g.AddNode(&model.Node{ID: "mod:src/a", Name: "a", Type: model.NodeModule, Path: "/repo/src"})
	g.AddNode(&model.Node{ID: "infra:database", Name: "Database", Type: model.NodeDatabase})
	g.AddNode(&model.Node{ID: "infra:queue", Name: "Queue", Type: model.NodeQueue})
	// Same pair of nodes, two edge types: the arrows stay separate and sorted by type.
	g.AddEdge(&model.Edge{Source: "mod:src/a", Target: "infra:database", Type: model.EdgeReadWrite, Label: "pg"})
	g.AddEdge(&model.Edge{Source: "mod:src/a", Target: "infra:database", Type: model.EdgeDependency, Label: "pg"})
	// A single edge between two nodes that are not folders keeps its label.
	g.AddEdge(&model.Edge{Source: "infra:queue", Target: "infra:database", Type: model.EdgeDataFlow, Label: "writes orders"})

	var got []string
	for _, e := range GroupByFolder(g, 2).Edges() {
		got = append(got, e.Source+" -> "+e.Target+" ["+string(e.Type)+"] "+e.Label)
	}
	sort.Strings(got)
	want := []string{
		"dir:src -> infra:database [" + string(model.EdgeDependency) + "] 1 import",
		"dir:src -> infra:database [" + string(model.EdgeReadWrite) + "] 1 read write",
		"infra:queue -> infra:database [" + string(model.EdgeDataFlow) + "] writes orders",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("edges =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
