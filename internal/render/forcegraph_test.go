package render

import (
	"strings"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func TestForceGraph_BasicOutput(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})
	g.AddNode(&model.Node{ID: "db:pg", Name: "Postgres", Type: model.NodeDatabase})
	g.AddEdge(&model.Edge{Source: "svc:api", Target: "db:pg", Type: model.EdgeReadWrite})

	out := ForceGraph(g, Options{Format: FormatHTML, ViewLevel: ViewContainer, Title: "Force test"})

	for _, want := range []string{
		"<!doctype html>",
		`<html lang="en">`,
		`<meta charset="utf-8">`,
		"<title>Force test</title>",
		"<svg id=\"chart\">",
		"const NODES =",
		"const LINKS =",
		"d3.forceSimulation",
		`"id":"svc:api"`,
		`"id":"db:pg"`,
		`"source":"svc:api"`,
		`"target":"db:pg"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q", want)
		}
	}
}

func TestForceGraph_DefaultTitle(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})

	out := ForceGraph(g, Options{Format: FormatHTML, ViewLevel: ViewContainer})

	if !strings.Contains(out, "<title>Architecture force-directed graph</title>") {
		t.Fatal("expected default title when Options.Title is empty")
	}
}

func TestForceGraph_EmbedsD3Runtime(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})

	out := ForceGraph(g, Options{Format: FormatHTML, ViewLevel: ViewContainer})

	// D3 minified bundle is around 270 KB. If the embedded asset goes
	// missing or is replaced with a stub, the page is broken; this size
	// floor catches that before it ships.
	if len(out) < 200_000 {
		t.Fatalf("output is %d bytes; expected at least 200 KB once D3 is embedded", len(out))
	}
}

func TestForceGraph_SelfContained(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})

	out := ForceGraph(g, Options{Format: FormatHTML, ViewLevel: ViewContainer})

	// No network references should slip into the rendered page; D3 is
	// inlined and the diagram data is in <script> blocks.
	for _, banned := range []string{
		`<script src=`,
		`<link href=`,
		`<link rel="stylesheet"`,
		`@import url`,
	} {
		if strings.Contains(out, banned) {
			t.Errorf("ForceGraph output should be self-contained; found external reference %q", banned)
		}
	}
}

func TestForceGraph_EmptyGraph(t *testing.T) {
	g := model.NewGraph("/tmp")

	out := ForceGraph(g, Options{Format: FormatHTML, ViewLevel: ViewContainer})

	// An empty graph should still produce a valid page with empty arrays
	// rather than panicking or producing malformed JSON.
	if !strings.Contains(out, "const NODES = [];") {
		t.Error("expected empty NODES array literal for empty graph")
	}
	if !strings.Contains(out, "const LINKS = [];") {
		t.Error("expected empty LINKS array literal for empty graph")
	}
	if !strings.Contains(out, "0 nodes, 0 edges, 0 connected components") {
		t.Error("expected meta line to report zero counts")
	}
}

func TestForceGraph_EscapesTitle(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})

	out := ForceGraph(g, Options{Format: FormatHTML, ViewLevel: ViewContainer, Title: "<script>x</script>"})

	if strings.Contains(out, "<title><script>x</script></title>") {
		t.Fatal("title was injected unescaped — XSS regression")
	}
	if !strings.Contains(out, "&lt;script&gt;x&lt;/script&gt;") {
		t.Fatal("expected title to be HTML-escaped")
	}
}

// connectedComponents tests below operate on hand-built VisibleGraphs so
// behavior is independent of FilterGraph / PrepareGraph layering.

func TestConnectedComponents_TwoIslands(t *testing.T) {
	// Three-node component {a,b,c} and two-node component {d,e}.
	vg := &VisibleGraph{
		Nodes: []*model.Node{
			{ID: "a", Name: "a", Type: model.NodeService},
			{ID: "b", Name: "b", Type: model.NodeService},
			{ID: "c", Name: "c", Type: model.NodeService},
			{ID: "d", Name: "d", Type: model.NodeService},
			{ID: "e", Name: "e", Type: model.NodeService},
		},
		Edges: []*model.Edge{
			{Source: "a", Target: "b"},
			{Source: "b", Target: "c"},
			{Source: "d", Target: "e"},
		},
	}

	got := connectedComponents(vg)

	// The 3-node island is the largest; it must be component 0.
	if got["a"] != 0 || got["b"] != 0 || got["c"] != 0 {
		t.Errorf("expected {a,b,c} in component 0, got a=%d b=%d c=%d", got["a"], got["b"], got["c"])
	}
	if got["d"] != 1 || got["e"] != 1 {
		t.Errorf("expected {d,e} in component 1, got d=%d e=%d", got["d"], got["e"])
	}
}

func TestConnectedComponents_TieBrokenByMinID(t *testing.T) {
	// Two equal-size islands: {a,b} and {x,y}. Lexicographic order on
	// the lowest member ID puts {a,b} before {x,y}.
	vg := &VisibleGraph{
		Nodes: []*model.Node{
			{ID: "x", Name: "x", Type: model.NodeService},
			{ID: "y", Name: "y", Type: model.NodeService},
			{ID: "a", Name: "a", Type: model.NodeService},
			{ID: "b", Name: "b", Type: model.NodeService},
		},
		Edges: []*model.Edge{
			{Source: "x", Target: "y"},
			{Source: "a", Target: "b"},
		},
	}

	got := connectedComponents(vg)

	if got["a"] != 0 || got["b"] != 0 {
		t.Errorf("expected {a,b} in component 0 (lower min-ID), got a=%d b=%d", got["a"], got["b"])
	}
	if got["x"] != 1 || got["y"] != 1 {
		t.Errorf("expected {x,y} in component 1, got x=%d y=%d", got["x"], got["y"])
	}
}

func TestConnectedComponents_FullyConnected(t *testing.T) {
	vg := &VisibleGraph{
		Nodes: []*model.Node{
			{ID: "a", Name: "a", Type: model.NodeService},
			{ID: "b", Name: "b", Type: model.NodeService},
			{ID: "c", Name: "c", Type: model.NodeService},
		},
		Edges: []*model.Edge{
			{Source: "a", Target: "b"},
			{Source: "b", Target: "c"},
		},
	}

	got := connectedComponents(vg)

	for _, id := range []string{"a", "b", "c"} {
		if got[id] != 0 {
			t.Errorf("expected %s in component 0, got %d", id, got[id])
		}
	}
}

func TestConnectedComponents_IgnoresDanglingEdges(t *testing.T) {
	// Edge target "ghost" is not in Nodes; union-find must skip it
	// rather than attaching the source to a phantom component.
	vg := &VisibleGraph{
		Nodes: []*model.Node{
			{ID: "a", Name: "a", Type: model.NodeService},
			{ID: "b", Name: "b", Type: model.NodeService},
		},
		Edges: []*model.Edge{
			{Source: "a", Target: "ghost"},
			{Source: "ghost", Target: "b"},
		},
	}

	got := connectedComponents(vg)

	// Both surviving nodes are isolated; tie-broken by min-ID.
	if got["a"] != 0 {
		t.Errorf("expected a in component 0, got %d", got["a"])
	}
	if got["b"] != 1 {
		t.Errorf("expected b in component 1 (separate island), got %d", got["b"])
	}
}

func TestComponentCount(t *testing.T) {
	cases := []struct {
		name  string
		nodes []fgNode
		want  int
	}{
		{
			name:  "empty",
			nodes: nil,
			want:  0,
		},
		{
			name:  "single component",
			nodes: []fgNode{{ID: "a", Component: 0}, {ID: "b", Component: 0}},
			want:  1,
		},
		{
			name:  "three components",
			nodes: []fgNode{{ID: "a", Component: 0}, {ID: "b", Component: 1}, {ID: "c", Component: 2}},
			want:  3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := componentCount(tc.nodes); got != tc.want {
				t.Errorf("componentCount(%v) = %d, want %d", tc.nodes, got, tc.want)
			}
		})
	}
}

func TestNodeTypeGroup_AllTypesMapped(t *testing.T) {
	g := nodeTypeGroup()

	// All node types referenced in forcegraph.go must have a group; an
	// unmapped type defaults to 0 (Service color), silently mis-categorizing
	// new node kinds. This guards against future model.NodeKind additions
	// being forgotten here.
	required := []model.NodeType{
		model.NodeService,
		model.NodeModule,
		model.NodePackage,
		model.NodeDatabase,
		model.NodeQueue,
		model.NodeCache,
		model.NodeExternalAPI,
		model.NodeEndpoint,
		model.NodeNote,
	}
	seen := make(map[int]model.NodeType)
	for _, nt := range required {
		idx, ok := g[nt]
		if !ok {
			t.Errorf("node type %q has no group assigned", nt)
			continue
		}
		if prev, dup := seen[idx]; dup {
			t.Errorf("node types %q and %q both map to group %d", prev, nt, idx)
		}
		seen[idx] = nt
	}
}
