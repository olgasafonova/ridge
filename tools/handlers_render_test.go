package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/render"
)

// renderFixtureGraph builds a small graph that survives the container view
// filter (no package or endpoint nodes) and carries enough edges for the prune
// and min-degree filters to have something to act on.
func renderFixtureGraph() *model.ArchGraph {
	g := model.NewGraph("/fixture")
	g.Topology = model.TopologyMonolith
	g.AddNode(&model.Node{ID: "api", Name: "api", Type: model.NodeService})
	g.AddNode(&model.Node{ID: "worker", Name: "worker", Type: model.NodeService})
	g.AddNode(&model.Node{ID: "db", Name: "db", Type: model.NodeDatabase})
	g.AddEdge(&model.Edge{Source: "api", Target: "db", Type: model.EdgeReadWrite, Confidence: 1})
	g.AddEdge(&model.Edge{Source: "worker", Target: "db", Type: model.EdgeReadWrite, Confidence: 1})
	return g
}

// TestBuildRenderOptions covers the per-field defaulting that archGenerate
// delegates to, including the HTML special case: HTML falls back to the
// component view because the container view renders near-empty for Go servers.
func TestBuildRenderOptions(t *testing.T) {
	base := render.DefaultOptions()

	tests := []struct {
		name string
		args ArchGenerateArgs
		want render.Options
	}{
		{
			name: "empty args keep the defaults",
			args: ArchGenerateArgs{},
			want: base,
		},
		{
			name: "explicit format leaves the default view level",
			args: ArchGenerateArgs{Format: "plantuml"},
			want: render.Options{Format: render.FormatPlantUML, ViewLevel: render.ViewContainer, Direction: "TB"},
		},
		{
			name: "html falls back to the component view",
			args: ArchGenerateArgs{Format: "html"},
			want: render.Options{Format: render.FormatHTML, ViewLevel: render.ViewComponent, Direction: "TB"},
		},
		{
			name: "explicit view level beats the html fallback",
			args: ArchGenerateArgs{Format: "html", ViewLevel: "system"},
			want: render.Options{Format: render.FormatHTML, ViewLevel: render.ViewSystem, Direction: "TB"},
		},
		{
			name: "display overrides are copied through",
			args: ArchGenerateArgs{Title: "My Arch", Direction: "LR", ThemeBG: "#0d1117", ThemeFG: "#e6edf3"},
			want: render.Options{
				Format:    render.FormatMermaid,
				ViewLevel: render.ViewContainer,
				Title:     "My Arch",
				Direction: "LR",
				Theme:     render.Theme{BG: "#0d1117", FG: "#e6edf3"},
			},
		},
		{
			name: "one theme colour is enough to set the theme",
			args: ArchGenerateArgs{ThemeFG: "#ffffff"},
			want: render.Options{
				Format:    render.FormatMermaid,
				ViewLevel: render.ViewContainer,
				Direction: "TB",
				Theme:     render.Theme{FG: "#ffffff"},
			},
		},
		{
			name: "positive filters are applied",
			args: ArchGenerateArgs{PruneThreshold: 0.5, MinDegree: 2},
			want: render.Options{
				Format:         render.FormatMermaid,
				ViewLevel:      render.ViewContainer,
				Direction:      "TB",
				PruneThreshold: 0.5,
				MinDegree:      2,
			},
		},
		{
			name: "non-positive filters are ignored rather than passed on",
			args: ArchGenerateArgs{PruneThreshold: -1, MinDegree: -3},
			want: base,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildRenderOptions(tt.args); got != tt.want {
				t.Errorf("buildRenderOptions() = %+v; want %+v", got, tt.want)
			}
		})
	}
}

// TestDispatchRenderer checks that every format in the renderFuncs table
// resolves to a renderer and produces output. The table is the dispatch, so a
// format added to the schema but missed here would show up as a gap.
func TestDispatchRenderer(t *testing.T) {
	graph := renderFixtureGraph()

	formats := []render.Format{
		render.FormatMermaid,
		render.FormatPlantUML,
		render.FormatC4,
		render.FormatStructurizr,
		render.FormatJSON,
		render.FormatDrawIO,
		render.FormatExcalidraw,
		render.FormatHTML,
		render.FormatForceGraph,
	}

	if len(formats) != len(renderFuncs) {
		t.Fatalf("renderFuncs has %d entries but this test covers %d; add the new format here", len(renderFuncs), len(formats))
	}

	for _, format := range formats {
		t.Run(string(format), func(t *testing.T) {
			opts := render.DefaultOptions()
			opts.Format = format
			out, err := dispatchRenderer(graph, opts)
			if err != nil {
				t.Fatalf("dispatchRenderer(%s) failed: %v", format, err)
			}
			if strings.TrimSpace(out) == "" {
				t.Errorf("dispatchRenderer(%s) returned empty output", format)
			}
		})
	}
}

// TestDispatchRenderer_UnsupportedFormat verifies the error names the format
// and lists the supported ones, so a caller that guessed a format can correct
// itself without another round trip.
func TestDispatchRenderer_UnsupportedFormat(t *testing.T) {
	opts := render.DefaultOptions()
	opts.Format = render.Format("graphviz")

	out, err := dispatchRenderer(renderFixtureGraph(), opts)
	if err == nil {
		t.Fatal("expected an error for an unsupported format, got nil")
	}
	if out != "" {
		t.Errorf("expected empty output alongside the error, got %q", out)
	}
	if !strings.Contains(err.Error(), "graphviz") {
		t.Errorf("error should name the rejected format, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "mermaid") {
		t.Errorf("error should list the supported formats, got %q", err.Error())
	}
}

// TestBuildPruneNotes covers the three outcomes: no filters configured, a
// super-node pruned by threshold, and the min-degree cascade that empties the
// diagram and has to say so.
func TestBuildPruneNotes(t *testing.T) {
	t.Run("no filters returns nothing", func(t *testing.T) {
		pruned, notes := buildPruneNotes(renderFixtureGraph(), render.DefaultOptions())
		if pruned != nil || notes != nil {
			t.Errorf("want nil, nil with filters off; got pruned=%v notes=%v", pruned, notes)
		}
	})

	t.Run("prune threshold reports the pruned node", func(t *testing.T) {
		opts := render.DefaultOptions()
		opts.PruneThreshold = 0.5

		// Both services write to db, so db's fan-in is 2 of 2 sources: above
		// the 0.5 threshold.
		pruned, notes := buildPruneNotes(renderFixtureGraph(), opts)
		if len(pruned) != 1 || pruned[0] != "db" {
			t.Errorf("want the db super-node pruned, got %v", pruned)
		}
		if notes != nil {
			t.Errorf("pruning alone should not add notes, got %v", notes)
		}
	})

	t.Run("min degree cascade to empty explains itself", func(t *testing.T) {
		graph := model.NewGraph("/fixture")
		graph.AddNode(&model.Node{ID: "lonely", Name: "lonely", Type: model.NodeService})
		opts := render.DefaultOptions()
		opts.MinDegree = 1

		_, notes := buildPruneNotes(graph, opts)
		if len(notes) != 1 {
			t.Fatalf("want one explanatory note when the cascade empties the graph, got %v", notes)
		}
		if !strings.Contains(notes[0], "min_degree=1") {
			t.Errorf("note should quote the offending min_degree, got %q", notes[0])
		}
	})
}

// TestMinDegreeCascadedToEmpty pins the predicate behind that note: it fires
// only when a positive min_degree wiped a graph that had nodes to begin with.
func TestMinDegreeCascadedToEmpty(t *testing.T) {
	nonEmptyGraph := renderFixtureGraph()
	emptyGraph := model.NewGraph("/fixture")

	tests := []struct {
		name      string
		minDegree int
		graph     *model.ArchGraph
		visible   *render.VisibleGraph
		want      bool
	}{
		{"filter disabled", 0, nonEmptyGraph, &render.VisibleGraph{}, false},
		{"nodes survived the filter", 2, nonEmptyGraph, &render.VisibleGraph{Nodes: nonEmptyGraph.Nodes()}, false},
		{"source graph was empty anyway", 2, emptyGraph, &render.VisibleGraph{}, false},
		{"filter emptied a non-empty graph", 2, nonEmptyGraph, &render.VisibleGraph{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := render.DefaultOptions()
			opts.MinDegree = tt.minDegree
			if got := minDegreeCascadedToEmpty(opts, tt.graph, tt.visible); got != tt.want {
				t.Errorf("minDegreeCascadedToEmpty() = %v; want %v", got, tt.want)
			}
		})
	}
}

// TestArchGenerate_RejectsBadInput covers the guards archGenerate runs before
// it scans anything.
func TestArchGenerate_RejectsBadInput(t *testing.T) {
	tests := []struct {
		name    string
		args    ArchGenerateArgs
		wantErr string
	}{
		{"neither path nor repo", ArchGenerateArgs{}, "either path or repo is required"},
		{"unknown repo alias", ArchGenerateArgs{Repo: "never-registered"}, "not found in registry"},
		{"path does not exist", ArchGenerateArgs{Path: "/nonexistent/ridge/fixture"}, "path does not exist"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newRegistryTestHandler(t)
			_, err := h.archGenerate(context.Background(), tt.args)
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q; want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestArchGenerate_RendersScannedCodebase is the happy path: scan a tiny Go
// module and render it, checking the result carries the resolved format, a
// diagram, and the graph summary.
func TestArchGenerate_RendersScannedCodebase(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.22\n")
	writeTestFile(t, dir, "cmd/api/main.go", "package main\n\nfunc main() {}\n")

	h := NewHandlerRegistry(testLogger())
	res, err := h.archGenerate(context.Background(), ArchGenerateArgs{Path: dir, ViewLevel: "component"})
	if err != nil {
		t.Fatalf("archGenerate failed: %v", err)
	}

	if res.Format != string(render.FormatMermaid) {
		t.Errorf("Format: want mermaid by default, got %q", res.Format)
	}
	if !strings.Contains(res.Diagram, "graph TB") {
		t.Errorf("expected a Mermaid graph body, got %q", res.Diagram)
	}
	if res.Summary == "" {
		t.Error("expected the graph summary to be carried into the result")
	}
	if res.PrunedNodes != nil || res.Notes != nil {
		t.Errorf("no filters were requested, so nothing should be pruned or noted: %v / %v", res.PrunedNodes, res.Notes)
	}
}

// TestArchGenerate_UnsupportedFormatReachesCaller checks the dispatch error
// survives the handler rather than being swallowed into an empty diagram.
func TestArchGenerate_UnsupportedFormatReachesCaller(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.22\n")

	h := NewHandlerRegistry(testLogger())
	_, err := h.archGenerate(context.Background(), ArchGenerateArgs{Path: dir, Format: "graphviz"})
	if err == nil {
		t.Fatal("expected an error for an unsupported format, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Errorf("error = %q; want it to mention the unsupported format", err.Error())
	}
}
