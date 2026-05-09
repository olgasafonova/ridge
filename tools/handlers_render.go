package tools

import (
	"context"
	"fmt"

	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/render"
	"github.com/olgasafonova/ridge/internal/safepath"
)

// =============================================================================
// arch_generate (diagram rendering)
// =============================================================================

// ArchGenerateArgs are the arguments for arch_generate.
type ArchGenerateArgs struct {
	Path           string  `json:"path"`
	Repo           string  `json:"repo,omitempty"`
	Format         string  `json:"format,omitempty"`
	ViewLevel      string  `json:"view_level,omitempty"`
	Title          string  `json:"title,omitempty"`
	Direction      string  `json:"direction,omitempty"`
	ThemeBG        string  `json:"theme_bg,omitempty"`
	ThemeFG        string  `json:"theme_fg,omitempty"`
	PruneThreshold float64 `json:"prune_threshold,omitempty"`
	MinDegree      int     `json:"min_degree,omitempty"`
	ScanControl
}

// ArchGenerateResult is the result of arch_generate.
type ArchGenerateResult struct {
	Format      string   `json:"format"`
	Diagram     string   `json:"diagram"`
	Summary     string   `json:"summary"`
	PrunedNodes []string `json:"pruned_nodes,omitempty"`
	Notes       []string `json:"notes,omitempty"`
}

func (h *HandlerRegistry) archGenerate(ctx context.Context, args ArchGenerateArgs) (*ArchGenerateResult, error) {
	path, alias, err := h.resolveRepoPath(args.Path, args.Repo)
	if err != nil {
		return nil, err
	}
	if err := safepath.ValidateScanPath(path); err != nil {
		return nil, err
	}

	graph, _, err := h.scanPath(ctx, path, alias, args.ScanControl)
	if err != nil {
		return nil, fmt.Errorf("scanning codebase: %w", err)
	}

	opts := buildRenderOptions(args)

	diagram, err := dispatchRenderer(graph, opts)
	if err != nil {
		return nil, err
	}

	prunedNodes, notes := buildPruneNotes(graph, opts)

	return &ArchGenerateResult{
		Format:      string(opts.Format),
		Diagram:     diagram,
		Summary:     graph.Summary(),
		PrunedNodes: prunedNodes,
		Notes:       notes,
	}, nil
}

// buildRenderOptions assembles render.Options from arch_generate args, applying
// per-field defaults. Pulled out of archGenerate to keep its complexity flat.
func buildRenderOptions(args ArchGenerateArgs) render.Options {
	opts := render.DefaultOptions()
	applyFormatAndView(&opts, args)
	applyDisplayOverrides(&opts, args)
	applyFilterOverrides(&opts, args)
	return opts
}

// applyFormatAndView sets the diagram format and view level. HTML output
// defaults to the component view because the container view produces
// near-empty output for Go MCP servers (packages and endpoints, no
// service-type nodes).
func applyFormatAndView(opts *render.Options, args ArchGenerateArgs) {
	if args.Format != "" {
		opts.Format = render.Format(args.Format)
	}
	if args.ViewLevel != "" {
		opts.ViewLevel = render.ViewLevel(args.ViewLevel)
		return
	}
	if opts.Format == render.FormatHTML {
		opts.ViewLevel = render.ViewComponent
	}
}

// applyDisplayOverrides copies non-empty title/direction/theme settings from
// args into opts.
func applyDisplayOverrides(opts *render.Options, args ArchGenerateArgs) {
	if args.Title != "" {
		opts.Title = args.Title
	}
	if args.Direction != "" {
		opts.Direction = args.Direction
	}
	if args.ThemeBG != "" || args.ThemeFG != "" {
		opts.Theme = render.Theme{BG: args.ThemeBG, FG: args.ThemeFG}
	}
}

// applyFilterOverrides copies positive prune-threshold and min-degree settings
// from args into opts.
func applyFilterOverrides(opts *render.Options, args ArchGenerateArgs) {
	if args.PruneThreshold > 0 {
		opts.PruneThreshold = args.PruneThreshold
	}
	if args.MinDegree > 0 {
		opts.MinDegree = args.MinDegree
	}
}

// renderFuncs maps each supported render format to its renderer function.
// Keeping the dispatch as data instead of a switch flattens archGenerate's
// cyclomatic complexity and makes adding a new format a one-line change.
var renderFuncs = map[render.Format]func(*model.ArchGraph, render.Options) string{
	render.FormatMermaid:     render.Mermaid,
	render.FormatPlantUML:    render.PlantUML,
	render.FormatC4:          render.C4,
	render.FormatStructurizr: render.Structurizr,
	render.FormatJSON:        render.JSON,
	render.FormatDrawIO:      render.DrawIO,
	render.FormatExcalidraw:  render.Excalidraw,
	render.FormatHTML:        render.HTML,
	render.FormatForceGraph:  render.ForceGraph,
}

func dispatchRenderer(graph *model.ArchGraph, opts render.Options) (string, error) {
	fn, ok := renderFuncs[opts.Format]
	if !ok {
		return "", fmt.Errorf("unsupported format: %s (supported: mermaid, plantuml, c4, structurizr, json, drawio, excalidraw, html, forcegraph)", opts.Format)
	}
	return fn(graph, opts), nil
}

// buildPruneNotes returns the list of nodes pruned by render filters and any
// caller-facing notes about silent filter cascades.
func buildPruneNotes(graph *model.ArchGraph, opts render.Options) ([]string, []string) {
	if opts.PruneThreshold <= 0 && opts.MinDegree <= 0 {
		return nil, nil
	}
	vg := render.PrepareGraph(graph, opts)
	var notes []string
	// min_degree iterates KeepHighDegree until stable, so on sparse
	// hub-and-spoke graphs a too-high threshold can cascade to zero
	// nodes silently. Tell the caller why the diagram is empty.
	if opts.MinDegree > 0 && len(vg.Nodes) == 0 && graph.NodeCount() > 0 {
		notes = append(notes, fmt.Sprintf(
			"min_degree=%d filtered out all %d nodes via iterative cascade (each round drops nodes below threshold, which lowers neighbor degrees, which drops more nodes). Try a lower min_degree, or omit it to see the full graph.",
			opts.MinDegree, graph.NodeCount()))
	}
	return vg.PrunedNodes, notes
}
