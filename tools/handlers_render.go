package tools

import (
	"context"
	"fmt"

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

	opts := render.DefaultOptions()
	if args.Format != "" {
		opts.Format = render.Format(args.Format)
	}
	if args.ViewLevel != "" {
		opts.ViewLevel = render.ViewLevel(args.ViewLevel)
	} else if opts.Format == render.FormatHTML {
		// Human-facing HTML defaults to the full component view; the
		// container default produces near-empty output for Go MCP
		// servers (packages and endpoints, no service-type nodes).
		opts.ViewLevel = render.ViewComponent
	}
	if args.Title != "" {
		opts.Title = args.Title
	}
	if args.Direction != "" {
		opts.Direction = args.Direction
	}
	if args.ThemeBG != "" || args.ThemeFG != "" {
		opts.Theme = render.Theme{BG: args.ThemeBG, FG: args.ThemeFG}
	}
	if args.PruneThreshold > 0 {
		opts.PruneThreshold = args.PruneThreshold
	}
	if args.MinDegree > 0 {
		opts.MinDegree = args.MinDegree
	}

	var diagram string
	switch opts.Format {
	case render.FormatMermaid:
		diagram = render.Mermaid(graph, opts)
	case render.FormatPlantUML:
		diagram = render.PlantUML(graph, opts)
	case render.FormatC4:
		diagram = render.C4(graph, opts)
	case render.FormatStructurizr:
		diagram = render.Structurizr(graph, opts)
	case render.FormatJSON:
		diagram = render.JSON(graph, opts)
	case render.FormatDrawIO:
		diagram = render.DrawIO(graph, opts)
	case render.FormatExcalidraw:
		diagram = render.Excalidraw(graph, opts)
	case render.FormatHTML:
		diagram = render.HTML(graph, opts)
	case render.FormatForceGraph:
		diagram = render.ForceGraph(graph, opts)
	default:
		return nil, fmt.Errorf("unsupported format: %s (supported: mermaid, plantuml, c4, structurizr, json, drawio, excalidraw, html, forcegraph)", args.Format)
	}

	// Report which nodes were pruned (if any) and surface filter warnings.
	var prunedNodes []string
	var notes []string
	if opts.PruneThreshold > 0 || opts.MinDegree > 0 {
		vg := render.PrepareGraph(graph, opts)
		prunedNodes = vg.PrunedNodes
		// min_degree iterates KeepHighDegree until stable, so on sparse
		// hub-and-spoke graphs a too-high threshold can cascade to zero
		// nodes silently. Tell the caller why the diagram is empty.
		if opts.MinDegree > 0 && len(vg.Nodes) == 0 && graph.NodeCount() > 0 {
			notes = append(notes, fmt.Sprintf(
				"min_degree=%d filtered out all %d nodes via iterative cascade (each round drops nodes below threshold, which lowers neighbor degrees, which drops more nodes). Try a lower min_degree, or omit it to see the full graph.",
				opts.MinDegree, graph.NodeCount()))
		}
	}

	return &ArchGenerateResult{
		Format:      string(opts.Format),
		Diagram:     diagram,
		Summary:     graph.Summary(),
		PrunedNodes: prunedNodes,
		Notes:       notes,
	}, nil
}
