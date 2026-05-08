package render

import (
	"strings"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func TestMermaid_BasicOutput(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:api", Name: "API Server", Type: model.NodeService})
	g.AddNode(&model.Node{ID: "db:postgres", Name: "PostgreSQL", Type: model.NodeDatabase})
	g.AddEdge(&model.Edge{Source: "svc:api", Target: "db:postgres", Type: model.EdgeReadWrite, Label: "queries"})

	result := Mermaid(g, DefaultOptions())

	if !strings.Contains(result, "graph TB") {
		t.Fatal("expected Mermaid graph TB directive")
	}
	if !strings.Contains(result, "API Server") {
		t.Fatal("expected service node in output")
	}
	if !strings.Contains(result, "PostgreSQL") {
		t.Fatal("expected database node in output")
	}
	if !strings.Contains(result, "queries") {
		t.Fatal("expected edge label in output")
	}
}

func TestMermaid_DatabaseShape(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "db:pg", Name: "PG", Type: model.NodeDatabase})

	result := Mermaid(g, Options{Format: FormatMermaid, ViewLevel: ViewContainer})

	// Database nodes use cylinder shape [(name)]
	if !strings.Contains(result, "[(") {
		t.Fatal("expected database cylinder shape [( in output")
	}
}

func TestMermaid_MinDegreeFilterIterative(t *testing.T) {
	g := model.NewGraph("/vault")
	// A triangle of hubs (each has degree 2 within the triangle), plus leaves.
	g.AddNode(&model.Node{ID: "note:h1", Name: "h1", Type: model.NodeNote})
	g.AddNode(&model.Node{ID: "note:h2", Name: "h2", Type: model.NodeNote})
	g.AddNode(&model.Node{ID: "note:h3", Name: "h3", Type: model.NodeNote})
	g.AddNode(&model.Node{ID: "note:leaf1", Name: "leaf1", Type: model.NodeNote})
	g.AddNode(&model.Node{ID: "note:leaf2", Name: "leaf2", Type: model.NodeNote})
	g.AddNode(&model.Node{ID: "note:lonely", Name: "lonely", Type: model.NodeNote})

	// Triangle: h1↔h2, h2↔h3, h3↔h1. Each hub has degree 2 within the triangle.
	g.AddEdge(&model.Edge{Source: "note:h1", Target: "note:h2", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "note:h2", Target: "note:h3", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "note:h3", Target: "note:h1", Type: model.EdgeDependency})
	// Leaves attached to h1.
	g.AddEdge(&model.Edge{Source: "note:leaf1", Target: "note:h1", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "note:leaf2", Target: "note:h1", Type: model.EdgeDependency})

	// At min_degree=2, leaves and the lonely node drop. Hubs stay (each
	// keeps degree 2 in the triangle even after leaves are removed).
	result := Mermaid(g, Options{ViewLevel: ViewContainer, MinDegree: 2})
	for _, hub := range []string{"([h1])", "([h2])", "([h3])"} {
		if !strings.Contains(result, hub) {
			t.Fatalf("hub %s should survive iterative pruning:\n%s", hub, result)
		}
	}
	if strings.Contains(result, "lonely") {
		t.Fatal("lonely note (degree=0) should be dropped")
	}
	for _, leaf := range []string{"([leaf1])", "([leaf2])"} {
		if strings.Contains(result, leaf) {
			t.Fatalf("leaf %s (degree=1) should be dropped", leaf)
		}
	}
}

func TestMermaid_MinDegreeIterativelyDropsMaroonedHub(t *testing.T) {
	// A "hub" whose only connections are to leaves below the threshold gets
	// dropped after iterative pruning, because losing its leaves leaves it
	// with no surviving edges.
	g := model.NewGraph("/vault")
	g.AddNode(&model.Node{ID: "note:hub", Name: "hub", Type: model.NodeNote})
	g.AddNode(&model.Node{ID: "note:a", Name: "a", Type: model.NodeNote})
	g.AddNode(&model.Node{ID: "note:b", Name: "b", Type: model.NodeNote})
	g.AddNode(&model.Node{ID: "note:c", Name: "c", Type: model.NodeNote})

	g.AddEdge(&model.Edge{Source: "note:a", Target: "note:hub", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "note:b", Target: "note:hub", Type: model.EdgeDependency})
	g.AddEdge(&model.Edge{Source: "note:c", Target: "note:hub", Type: model.EdgeDependency})

	result := Mermaid(g, Options{ViewLevel: ViewContainer, MinDegree: 3})
	if strings.Contains(result, "hub") {
		t.Fatalf("hub of dropped leaves should be removed by iterative pruning:\n%s", result)
	}
}

func TestMermaid_NoteShape(t *testing.T) {
	g := model.NewGraph("/vault")
	g.AddNode(&model.Node{ID: "note:topic/intro", Name: "intro", Type: model.NodeNote})

	result := Mermaid(g, Options{ViewLevel: ViewContainer})
	if !strings.Contains(result, "subgraph Notes") {
		t.Fatal("expected Notes subgraph")
	}
	if !strings.Contains(result, "([intro])") {
		t.Fatalf("expected stadium shape ([intro]) in output:\n%s", result)
	}
}

func TestMermaid_NoteLabelWithParensIsQuoted(t *testing.T) {
	g := model.NewGraph("/vault")
	g.AddNode(&model.Node{
		ID:   "note:topic/article",
		Name: "Article (with parens) - Author",
		Type: model.NodeNote,
	})

	result := Mermaid(g, Options{ViewLevel: ViewContainer})
	if !strings.Contains(result, `(["Article (with parens) - Author"])`) {
		t.Fatalf("expected quoted label inside stadium shape:\n%s", result)
	}
}

func TestMermaid_NotesAtSystemLevelHidden(t *testing.T) {
	g := model.NewGraph("/vault")
	g.AddNode(&model.Node{ID: "note:n", Name: "note-name", Type: model.NodeNote})
	g.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})

	result := Mermaid(g, Options{ViewLevel: ViewSystem})
	if strings.Contains(result, "note-name") {
		t.Fatal("system view should hide notes")
	}
	if !strings.Contains(result, "API") {
		t.Fatal("system view should still show services")
	}
}

func TestMermaid_ViewLevelFiltering(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})
	g.AddNode(&model.Node{ID: "pkg:utils", Name: "utils", Type: model.NodePackage})
	g.AddNode(&model.Node{ID: "ep:health", Name: "GET /health", Type: model.NodeEndpoint})

	// System level: only services and external APIs
	sysResult := Mermaid(g, Options{ViewLevel: ViewSystem})
	if strings.Contains(sysResult, "utils") {
		t.Fatal("system view should not show packages")
	}
	if strings.Contains(sysResult, "/health") {
		t.Fatal("system view should not show endpoints")
	}

	// Component level: everything
	compResult := Mermaid(g, Options{ViewLevel: ViewComponent})
	if !strings.Contains(compResult, "utils") {
		t.Fatal("component view should show packages")
	}
	if !strings.Contains(compResult, "/health") {
		t.Fatal("component view should show endpoints")
	}
}

func TestMermaid_CustomTitle(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})

	result := Mermaid(g, Options{Title: "My Architecture", ViewLevel: ViewContainer})
	if !strings.Contains(result, "title: My Architecture") {
		t.Fatal("expected custom title in output")
	}
}

func TestMermaid_LRDirection(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})

	result := Mermaid(g, Options{Direction: "LR", ViewLevel: ViewContainer})
	if !strings.Contains(result, "graph LR") {
		t.Fatal("expected LR direction")
	}
}

func TestMermaid_ImportEdgeResolution(t *testing.T) {
	// Simulate what the Go analyzer produces: package nodes with absolute paths,
	// and dependency edges targeting "import:" prefixed full module paths.
	g := model.NewGraph("/home/user/myproject")
	g.AddNode(&model.Node{
		ID:   "pkg:tools/tools",
		Name: "tools",
		Type: model.NodePackage,
		Path: "/home/user/myproject/tools",
	})
	g.AddNode(&model.Node{
		ID:   "pkg:model/model",
		Name: "model",
		Type: model.NodePackage,
		Path: "/home/user/myproject/internal/model",
	})
	g.AddNode(&model.Node{
		ID:   "pkg:render/render",
		Name: "render",
		Type: model.NodePackage,
		Path: "/home/user/myproject/internal/render",
	})

	// Internal import edges (should resolve to package nodes)
	g.AddEdge(&model.Edge{
		Source: "pkg:tools/tools",
		Target: "import:github.com/user/myproject/internal/model",
		Type:   model.EdgeDependency,
		Label:  "github.com/user/myproject/internal/model",
	})
	g.AddEdge(&model.Edge{
		Source: "pkg:tools/tools",
		Target: "import:github.com/user/myproject/internal/render",
		Type:   model.EdgeDependency,
		Label:  "github.com/user/myproject/internal/render",
	})
	g.AddEdge(&model.Edge{
		Source: "pkg:render/render",
		Target: "import:github.com/user/myproject/internal/model",
		Type:   model.EdgeDependency,
		Label:  "github.com/user/myproject/internal/model",
	})

	// External import edge (should NOT appear — no matching node)
	g.AddEdge(&model.Edge{
		Source: "pkg:tools/tools",
		Target: "import:fmt",
		Type:   model.EdgeDependency,
		Label:  "fmt",
	})

	result := Mermaid(g, Options{ViewLevel: ViewComponent})

	// Resolved internal edges should produce arrows
	if !strings.Contains(result, "-->") {
		t.Fatalf("expected resolved edges in Mermaid output, got:\n%s", result)
	}

	// Should have edges from tools to model and render
	toolsID := SanitizeID("pkg:tools/tools")
	modelID := SanitizeID("pkg:model/model")
	renderID := SanitizeID("pkg:render/render")

	if !strings.Contains(result, toolsID+" -->") {
		t.Errorf("expected edge from tools, got:\n%s", result)
	}
	if !strings.Contains(result, modelID) {
		t.Errorf("expected model node in output, got:\n%s", result)
	}
	if !strings.Contains(result, renderID) {
		t.Errorf("expected render node in output, got:\n%s", result)
	}

	// External import "fmt" should NOT produce an edge
	if strings.Contains(result, "fmt") {
		t.Errorf("external import 'fmt' should not appear as edge, got:\n%s", result)
	}
}

func TestMermaid_SuppressesRedundantLabels(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})
	g.AddNode(&model.Node{ID: "svc:db", Name: "DB", Type: model.NodeService})
	g.AddEdge(&model.Edge{
		Source: "svc:api", Target: "svc:db",
		Type: model.EdgeDependency, Label: "dependency",
	})

	result := Mermaid(g, Options{ViewLevel: ViewContainer})

	// "dependency" label should be suppressed (matches edge type)
	if strings.Contains(result, "|dependency|") {
		t.Error("expected 'dependency' label to be suppressed")
	}
	// Edge should still exist, just without label
	if !strings.Contains(result, "-->") {
		t.Error("expected edge arrow in output")
	}
}

func TestMermaid_ThemeApplied(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})

	opts := Options{
		ViewLevel: ViewContainer,
		Theme:     Theme{BG: "#ffffff", FG: "#1e293b"},
	}
	result := Mermaid(g, opts)

	if !strings.Contains(result, "%%{init:") {
		t.Fatal("expected %%{init: theme directive in output")
	}
	if !strings.Contains(result, "'theme': 'base'") {
		t.Fatal("expected base theme in init directive")
	}
	if !strings.Contains(result, "'primaryTextColor': '#1e293b'") {
		t.Fatal("expected FG color as primaryTextColor")
	}
	if !strings.Contains(result, "'titleColor': '#1e293b'") {
		t.Fatal("expected FG color as titleColor")
	}
}

func TestMermaid_NoThemeWhenEmpty(t *testing.T) {
	g := model.NewGraph("/tmp")
	g.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})

	result := Mermaid(g, DefaultOptions())

	if strings.Contains(result, "%%{init:") {
		t.Fatal("expected no theme directive when Theme is empty")
	}
}

func TestParseHex(t *testing.T) {
	tests := []struct {
		input   string
		r, g, b uint8
		ok      bool
	}{
		{"#ffffff", 255, 255, 255, true},
		{"#000000", 0, 0, 0, true},
		{"#1e293b", 30, 41, 59, true},
		{"fff", 255, 255, 255, true},  // shorthand without #
		{"#abc", 170, 187, 204, true}, // shorthand with #
		{"invalid", 0, 0, 0, false},
		{"#gg0000", 0, 0, 0, false},
	}
	for _, tt := range tests {
		r, g, b, ok := parseHex(tt.input)
		if ok != tt.ok {
			t.Errorf("parseHex(%q): ok=%v, want %v", tt.input, ok, tt.ok)
			continue
		}
		if ok && (r != tt.r || g != tt.g || b != tt.b) {
			t.Errorf("parseHex(%q) = (%d,%d,%d), want (%d,%d,%d)",
				tt.input, r, g, b, tt.r, tt.g, tt.b)
		}
	}
}

func TestMermaidThemeInit_ColorMixing(t *testing.T) {
	// White BG + black FG: 3% mix should be very close to white
	result := mermaidThemeInit(Theme{BG: "#ffffff", FG: "#000000"})

	// 3% of black into white = rgb(247,247,247) = #f7f7f7
	if !strings.Contains(result, "#f7f7f7") {
		t.Errorf("expected #f7f7f7 for 3%% black into white, got:\n%s", result)
	}
	// 30% of black into white = rgb(178,178,178) = #b2b2b2
	if !strings.Contains(result, "#b2b2b2") {
		t.Errorf("expected #b2b2b2 for 30%% black into white, got:\n%s", result)
	}
}

func TestSanitizeID(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"svc:api", "svc___api"},
		{"pkg/utils", "pkg__utils"},
		{"node.name", "node_name"},
		{"my-service", "my_service"},
		// Verify different separators produce distinct IDs
		{"api/v1", "api__v1"},
		{"api.v1", "api_v1"},
		{"api:v1", "api___v1"},
	}
	for _, tt := range tests {
		got := SanitizeID(tt.input)
		if got != tt.expected {
			t.Errorf("SanitizeID(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
