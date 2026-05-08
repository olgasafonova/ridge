package render

import (
	"sort"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func TestPruneSuperNodes_Disabled(t *testing.T) {
	vg := makeTestVisibleGraph()
	pruned := PruneSuperNodes(vg, 0)
	if len(pruned) != 0 {
		t.Fatalf("expected no pruning when threshold=0, got %v", pruned)
	}
	if len(vg.Nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(vg.Nodes))
	}
}

func TestPruneSuperNodes_PrunesHighFanIn(t *testing.T) {
	// Graph: A->logging, B->logging, C->logging, A->B
	// logging has fan-in 3/3 = 100%, well above 0.5 threshold
	vg := makeTestVisibleGraph()
	pruned := PruneSuperNodes(vg, 0.5)

	if len(pruned) != 1 || pruned[0] != "logging" {
		t.Fatalf("expected [logging] pruned, got %v", pruned)
	}

	// Verify logging node is removed
	for _, n := range vg.Nodes {
		if n.ID == "pkg:logging" {
			t.Fatal("logging node should have been removed")
		}
	}

	// Verify edges to/from logging are removed
	for _, e := range vg.Edges {
		if e.Source == "pkg:logging" || e.Target == "pkg:logging" {
			t.Fatal("edges involving logging should have been removed")
		}
	}

	// A->B edge should survive
	if len(vg.Edges) != 1 {
		t.Fatalf("expected 1 remaining edge (A->B), got %d", len(vg.Edges))
	}
}

func TestPruneSuperNodes_ThresholdAt100(t *testing.T) {
	// Threshold 1.0 means prune only nodes with ratio > 1.0, which is impossible
	vg := makeTestVisibleGraph()
	pruned := PruneSuperNodes(vg, 1.0)
	if len(pruned) != 0 {
		t.Fatalf("threshold 1.0 should prune nothing, got %v", pruned)
	}
}

func TestPruneSuperNodes_NoEdges(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:a", Name: "A", Type: model.NodeService})
	vg := FilterGraph(g, ViewComponent)
	pruned := PruneSuperNodes(vg, 0.5)
	if len(pruned) != 0 {
		t.Fatalf("expected no pruning with no edges, got %v", pruned)
	}
}

func TestPruneSuperNodes_MultipleSuperNodes(t *testing.T) {
	// A->fmt, B->fmt, C->fmt, A->errors, B->errors, C->errors
	// Both fmt and errors have fan-in 3/3 = 100%
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "pkg:a", Name: "A", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:b", Name: "B", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:c", Name: "C", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:fmt", Name: "fmt", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:errors", Name: "errors", Type: model.NodePackage})
	g.AddEdge(&model.Edge{Source: "pkg:a", Target: "pkg:fmt", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:b", Target: "pkg:fmt", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:c", Target: "pkg:fmt", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:a", Target: "pkg:errors", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:b", Target: "pkg:errors", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:c", Target: "pkg:errors", Type: model.EdgeDependency})

	vg := FilterGraph(g, ViewComponent)
	pruned := PruneSuperNodes(vg, 0.5)
	sort.Strings(pruned)

	if len(pruned) != 2 {
		t.Fatalf("expected 2 pruned nodes, got %v", pruned)
	}
	if pruned[0] != "errors" || pruned[1] != "fmt" {
		t.Fatalf("expected [errors, fmt], got %v", pruned)
	}
	if len(vg.Nodes) != 3 {
		t.Fatalf("expected 3 remaining nodes, got %d", len(vg.Nodes))
	}
	if len(vg.Edges) != 0 {
		t.Fatalf("expected 0 remaining edges, got %d", len(vg.Edges))
	}
}

func TestPrepareGraph_WithPruning(t *testing.T) {
	g := makeTestGraph()
	opts := Options{ViewLevel: ViewComponent, PruneThreshold: 0.5}
	vg := PrepareGraph(g, opts)

	if len(vg.PrunedNodes) != 1 || vg.PrunedNodes[0] != "logging" {
		t.Fatalf("expected [logging] in PrunedNodes, got %v", vg.PrunedNodes)
	}
}

func TestPrepareGraph_WithoutPruning(t *testing.T) {
	g := makeTestGraph()
	opts := Options{ViewLevel: ViewComponent}
	vg := PrepareGraph(g, opts)

	if len(vg.PrunedNodes) != 0 {
		t.Fatalf("expected no pruned nodes, got %v", vg.PrunedNodes)
	}
	if len(vg.Nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(vg.Nodes))
	}
}

// makeTestGraph builds A->logging, B->logging, C->logging, A->B
func makeTestGraph() *model.ArchGraph {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "pkg:a", Name: "A", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:b", Name: "B", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:c", Name: "C", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:logging", Name: "logging", Type: model.NodePackage})
	g.AddEdge(&model.Edge{Source: "pkg:a", Target: "pkg:logging", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:b", Target: "pkg:logging", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:c", Target: "pkg:logging", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:a", Target: "pkg:b", Type: model.EdgeDependency})
	return g
}

func makeTestVisibleGraph() *VisibleGraph {
	return FilterGraph(makeTestGraph(), ViewComponent)
}

// TestKeepHighDegree_CascadeToEmpty documents the iterative-cascade behavior:
// on a sparse hub-and-spoke graph, a too-high min_degree drops leaves first,
// which lowers hub degree, which drops the hub on the next iteration. The
// arch_generate handler relies on this collapse-to-zero condition to surface
// a warning so callers know why their diagram is empty.
func TestKeepHighDegree_CascadeToEmpty(t *testing.T) {
	// Hub with 3 leaves: hub has degree 3, each leaf has degree 1.
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "pkg:hub", Name: "hub", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:l1", Name: "l1", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:l2", Name: "l2", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:l3", Name: "l3", Type: model.NodePackage})
	g.AddEdge(&model.Edge{Source: "pkg:l1", Target: "pkg:hub", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:l2", Target: "pkg:hub", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:l3", Target: "pkg:hub", Type: model.EdgeDependency})

	vg := FilterGraph(g, ViewComponent)
	KeepHighDegree(vg, 4) // higher than hub's degree of 3

	if len(vg.Nodes) != 0 {
		t.Fatalf("expected cascade to drop everything, got %d nodes", len(vg.Nodes))
	}
}

func TestKeepHighDegree_KeepsDenseSubgraph(t *testing.T) {
	// Triangle (each node degree 2) plus an outer leaf.
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "pkg:a", Name: "A", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:b", Name: "B", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:c", Name: "C", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:leaf", Name: "leaf", Type: model.NodePackage})
	g.AddEdge(&model.Edge{Source: "pkg:a", Target: "pkg:b", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:b", Target: "pkg:c", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:c", Target: "pkg:a", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:leaf", Target: "pkg:a", Type: model.EdgeDependency})

	vg := FilterGraph(g, ViewComponent)
	KeepHighDegree(vg, 2) // leaf has degree 1, triangle nodes have degree 2 or 3

	// Leaf drops in iteration 1; A loses degree 1 (3→2), still meets threshold.
	// Triangle survives.
	if len(vg.Nodes) != 3 {
		t.Fatalf("expected triangle to survive (3 nodes), got %d", len(vg.Nodes))
	}
	for _, n := range vg.Nodes {
		if n.ID == "pkg:leaf" {
			t.Fatal("leaf should have been filtered")
		}
	}
}

func TestFilterGraph_DeduplicatesResolvedEdges(t *testing.T) {
	g := model.NewGraph("/project")
	g.AddNode(&model.Node{
		ID:   "pkg:a/a",
		Name: "a",
		Type: model.NodePackage,
		Path: "/project/a",
	})
	g.AddNode(&model.Node{
		ID:   "pkg:b/b",
		Name: "b",
		Type: model.NodePackage,
		Path: "/project/b",
	})

	// Two files in package "a" both import package "b" — produces duplicate edges
	g.AddEdge(&model.Edge{
		Source: "pkg:a/a",
		Target: "import:github.com/x/project/b",
		Type:   model.EdgeDependency,
	})
	g.AddEdge(&model.Edge{
		Source: "pkg:a/a",
		Target: "import:github.com/x/project/b",
		Type:   model.EdgeDependency,
	})

	vg := FilterGraph(g, ViewComponent)

	if len(vg.Edges) != 1 {
		t.Errorf("expected 1 deduplicated edge, got %d", len(vg.Edges))
	}
}

func TestFilterGraph_SkipsSelfEdges(t *testing.T) {
	g := model.NewGraph("/project")
	g.AddNode(&model.Node{
		ID:   "pkg:a/a",
		Name: "a",
		Type: model.NodePackage,
		Path: "/project/a",
	})

	// An edge that resolves to the same package (self-import)
	g.AddEdge(&model.Edge{
		Source: "pkg:a/a",
		Target: "import:github.com/x/project/a",
		Type:   model.EdgeDependency,
	})

	vg := FilterGraph(g, ViewComponent)

	if len(vg.Edges) != 0 {
		t.Errorf("expected 0 edges (self-edge skipped), got %d", len(vg.Edges))
	}
}

func TestTransitiveReduce_RemovesRedundantEdges(t *testing.T) {
	// A → B → C, plus direct A → C (redundant)
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "pkg:a", Name: "a", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:b", Name: "b", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "pkg:c", Name: "c", Type: model.NodePackage})
	g.AddEdge(&model.Edge{Source: "pkg:a", Target: "pkg:b", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:b", Target: "pkg:c", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:a", Target: "pkg:c", Type: model.EdgeDependency}) // redundant

	vg := FilterGraph(g, ViewComponent)
	if len(vg.Edges) != 3 {
		t.Fatalf("before reduction: expected 3 edges, got %d", len(vg.Edges))
	}

	vg.TransitiveReduce()

	if len(vg.Edges) != 2 {
		t.Fatalf("after reduction: expected 2 edges, got %d", len(vg.Edges))
	}

	// A→C should be removed, A→B and B→C should remain
	for _, e := range vg.Edges {
		if e.Source == "pkg:a" && e.Target == "pkg:c" {
			t.Error("transitive edge A→C should have been removed")
		}
	}
}

func TestTransitiveReduce_PreservesDifferentTypes(t *testing.T) {
	// A → B (dependency), B → C (dependency), A → C (api_call)
	// A→C is NOT redundant because it's a different edge type.
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:a", Name: "a", Type: model.NodeService})
	g.AddNode(&model.Node{ID: "svc:b", Name: "b", Type: model.NodeService})
	g.AddNode(&model.Node{ID: "svc:c", Name: "c", Type: model.NodeService})
	g.AddEdge(&model.Edge{Source: "svc:a", Target: "svc:b", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "svc:b", Target: "svc:c", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "svc:a", Target: "svc:c", Type: model.EdgeAPICall})

	vg := FilterGraph(g, ViewContainer)
	vg.TransitiveReduce()

	if len(vg.Edges) != 3 {
		t.Fatalf("expected all 3 edges preserved (different types), got %d", len(vg.Edges))
	}
}

func TestTransitiveReduce_DeepChain(t *testing.T) {
	// A → B → C → D, plus A → C and A → D (both redundant)
	g := model.NewGraph("/tmp")
	for _, id := range []string{"a", "b", "c", "d"} {
		g.AddNode(&model.Node{ID: "pkg:" + id, Name: id, Type: model.NodePackage})
	}
	g.AddEdge(&model.Edge{Source: "pkg:a", Target: "pkg:b", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:b", Target: "pkg:c", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:c", Target: "pkg:d", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "pkg:a", Target: "pkg:c", Type: model.EdgeDependency}) // redundant
	g.AddEdge(&model.Edge{Source: "pkg:a", Target: "pkg:d", Type: model.EdgeDependency}) // redundant

	vg := FilterGraph(g, ViewComponent)
	vg.TransitiveReduce()

	if len(vg.Edges) != 3 {
		t.Fatalf("expected 3 edges (chain only), got %d", len(vg.Edges))
	}
}

func TestBarycenterOrder_ReducesCrossings(t *testing.T) {
	// Layer 0 (top): A, B
	// Layer 1 (bottom): C, D
	// Edges: A→D, B→C (crossing if layers stay [A,B],[C,D])
	// After barycenter: layer 1 should become [D,C] to uncross.
	a := &model.Node{ID: "a", Name: "a", Type: model.NodePackage}
	b := &model.Node{ID: "b", Name: "b", Type: model.NodePackage}
	c := &model.Node{ID: "c", Name: "c", Type: model.NodePackage}
	d := &model.Node{ID: "d", Name: "d", Type: model.NodePackage}

	layers := [][]*model.Node{
		{a, b}, // layer 0
		{c, d}, // layer 1
	}
	edges := []*model.Edge{
		{Source: "a", Target: "d", Type: model.EdgeDependency},
		{Source: "b", Target: "c", Type: model.EdgeDependency},
	}

	BarycenterOrder(layers, edges)

	// After ordering, layer 1 should be [D, C] (D first since A is at pos 0)
	if layers[1][0].ID != "d" || layers[1][1].ID != "c" {
		t.Errorf("expected layer 1 = [d, c], got [%s, %s]",
			layers[1][0].ID, layers[1][1].ID)
	}
}

func TestEdgeLabel(t *testing.T) {
	tests := []struct {
		label      string
		edgeType   model.EdgeType
		targetName string
		want       string
	}{
		// Empty label → empty
		{"", model.EdgeDependency, "utils", ""},
		// Label matches edge type name → suppressed
		{"dependency", model.EdgeDependency, "utils", ""},
		// Label matches target node name → suppressed
		{"utils", model.EdgeDependency, "utils", ""},
		// Meaningful label → kept
		{"queries", model.EdgeReadWrite, "PostgreSQL", "queries"},
		// Label different from type and target → kept
		{"HTTP", model.EdgeAPICall, "gateway", "HTTP"},
	}
	for _, tt := range tests {
		e := &model.Edge{Type: tt.edgeType, Label: tt.label}
		got := EdgeLabel(e, tt.targetName)
		if got != tt.want {
			t.Errorf("EdgeLabel(label=%q, type=%q, target=%q) = %q, want %q",
				tt.label, tt.edgeType, tt.targetName, got, tt.want)
		}
	}
}
