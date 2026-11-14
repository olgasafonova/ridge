package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/render"
	"github.com/olgasafonova/ridge/internal/safepath"
)

// =============================================================================
// arch_generate (diagram rendering)
// =============================================================================

// ArchGenerateArgs are the arguments for arch_generate.
type ArchGenerateArgs struct {
	Path           string  `json:"path" jsonschema:"Absolute filesystem path to the codebase to analyze. Supply either path or repo; path takes precedence when both are set."`
	Repo           string  `json:"repo,omitempty" jsonschema:"Alias of a codebase previously registered with arch_registry_add, used instead of path. Supply either path or repo; path takes precedence when both are set."`
	Format         string  `json:"format,omitempty" jsonschema:"Diagram output format: mermaid (default), plantuml, c4, structurizr, json, drawio, excalidraw, html, or forcegraph."`
	ViewLevel      string  `json:"view_level,omitempty" jsonschema:"Abstraction level: system (service overview), container (services, databases and queues; the default), or component (internal packages and modules). HTML output defaults to component instead, because container renders near-empty for Go MCP servers."`
	Title          string  `json:"title,omitempty" jsonschema:"Title rendered on the diagram. Defaults to a generated title when omitted."`
	Direction      string  `json:"direction,omitempty" jsonschema:"Layout direction, in Mermaid terms: TB (top to bottom, the default), LR, RL, or BT. Only mermaid and plantuml honour it."`
	ThemeBG        string  `json:"theme_bg,omitempty" jsonschema:"Background colour for the rendered diagram, as a CSS colour (e.g. #0d1117)."`
	ThemeFG        string  `json:"theme_fg,omitempty" jsonschema:"Foreground and text colour for the rendered diagram, as a CSS colour (e.g. #e6edf3)."`
	PruneThreshold float64 `json:"prune_threshold,omitempty" jsonschema:"Drop nodes whose relative importance falls below this fraction (0 to 1), to thin a dense diagram. Pruned node names are listed in the result. Unset disables pruning."`
	MinDegree      int     `json:"min_degree,omitempty" jsonschema:"Drop nodes with fewer than this many connected edges. Useful for hiding leaf nodes on a large graph; the result notes when this emptied the diagram. Unset disables the filter."`
	GroupDepth     int     `json:"group_depth,omitempty" jsonschema:"Collapse files into one box per folder, cut to this many path segments below the scan root (2 gives src/lib, src/routes). Arrows between folders are labelled with the number of file-to-file links they merge. Use on large module graphs. Unset keeps one box per file."`
	MaxChars       int     `json:"max_chars,omitempty" jsonschema:"Longest diagram to return, in characters. Omitted or 0 uses the default of 100000. A longer diagram is cut, truncated is set, total_chars gives the full length, and a note names the max_chars that returns it whole. HTML output runs to about 900000 characters and forcegraph to about 290000, so raise this for those formats."`
	ScanControl
}

// ArchGenerateResult is the result of arch_generate.
type ArchGenerateResult struct {
	Format      string   `json:"format"`
	Diagram     string   `json:"diagram"`
	Summary     string   `json:"summary"`
	PrunedNodes []string `json:"pruned_nodes,omitempty"`
	Notes       []string `json:"notes,omitempty"`
	// Truncated is true when Diagram was cut to the max_chars limit.
	Truncated bool `json:"truncated,omitempty"`
	// TotalChars is the length of the full rendered diagram in characters,
	// before any cut. It equals len(Diagram) in characters when Truncated
	// is false.
	TotalChars int `json:"total_chars"`
}

// DefaultMaxChars is the diagram length arch_generate returns when the caller
// sets no max_chars. 100000 characters is roughly 25000 tokens, the default
// MCP output budget in Claude Code, so a default response fits a client's
// window instead of being spilled or dropped. Follows mediawiki-mcp-server's
// CharacterLimit + Truncated pattern (wiki/read.go).
const DefaultMaxChars = 100_000

func (h *HandlerRegistry) archGenerate(ctx context.Context, args ArchGenerateArgs) (*ArchGenerateResult, error) {
	if args.MaxChars < 0 {
		return nil, fmt.Errorf("max_chars must be 0 or more, got %d (omit it or pass 0 for the default of %d)", args.MaxChars, DefaultMaxChars)
	}
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
	graph = render.GroupByFolder(graph, opts.GroupDepth)

	diagram, err := dispatchRenderer(graph, opts)
	if err != nil {
		return nil, err
	}

	prunedNodes, notes := buildPruneNotes(graph, opts)

	res := &ArchGenerateResult{
		Format:      string(opts.Format),
		Diagram:     diagram,
		Summary:     graph.Summary(),
		PrunedNodes: prunedNodes,
		Notes:       notes,
	}
	applyCharLimit(res, args.MaxChars)
	return res, nil
}

// applyCharLimit cuts res.Diagram to maxChars characters (DefaultMaxChars when
// maxChars is 0), records the full length in TotalChars, and on a cut sets
// Truncated and appends a note naming the max_chars that returns the whole
// diagram and the arguments that shrink it instead.
func applyCharLimit(res *ArchGenerateResult, maxChars int) {
	limit := maxChars
	if limit <= 0 {
		limit = DefaultMaxChars
	}
	res.TotalChars = utf8.RuneCountInString(res.Diagram)
	if res.TotalChars <= limit {
		return
	}
	res.Diagram = truncateRunes(res.Diagram, limit)
	res.Truncated = true
	res.Notes = append(res.Notes, fmt.Sprintf(
		"diagram truncated to %d of %d characters (%.1f%%). Pass max_chars=%d to get it whole, or shrink it with group_depth, min_degree, prune_threshold, a coarser view_level, or arch_focus on a subdirectory. A cut %s diagram is incomplete and may not parse.",
		limit, res.TotalChars, float64(limit)/float64(res.TotalChars)*100, res.TotalChars, res.Format))
}

// truncateRunes returns the first n characters of s, cutting on a character
// boundary so a multi-byte label is never split into invalid UTF-8.
func truncateRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
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
	if args.GroupDepth > 0 {
		opts.GroupDepth = args.GroupDepth
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

// SupportedFormats returns the sorted names of every format renderFuncs can
// dispatch to. Used to derive the "Output Formats" section of the server
// instructions and the unsupported-format error from the live dispatch map
// instead of a hand-maintained list.
func SupportedFormats() []string {
	formats := make([]string, 0, len(renderFuncs))
	for format := range renderFuncs {
		formats = append(formats, string(format))
	}
	slices.Sort(formats)
	return formats
}

func dispatchRenderer(graph *model.ArchGraph, opts render.Options) (string, error) {
	fn, ok := renderFuncs[opts.Format]
	if !ok {
		return "", fmt.Errorf("unsupported format: %s (supported: %s)", opts.Format, strings.Join(SupportedFormats(), ", "))
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
	if minDegreeCascadedToEmpty(opts, graph, vg) {
		notes = append(notes, fmt.Sprintf(
			"min_degree=%d filtered out all %d nodes via iterative cascade (each round drops nodes below threshold, which lowers neighbor degrees, which drops more nodes). Try a lower min_degree, or omit it to see the full graph.",
			opts.MinDegree, graph.NodeCount()))
	}
	return vg.PrunedNodes, notes
}

// minDegreeCascadedToEmpty reports whether a positive MinDegree caused the
// iterative KeepHighDegree filter to wipe out every node despite the source
// graph being non-empty. Pulled into a predicate so the caller's guard reads
// as one clause.
func minDegreeCascadedToEmpty(opts render.Options, graph *model.ArchGraph, vg *render.VisibleGraph) bool {
	if opts.MinDegree <= 0 {
		return false
	}
	return len(vg.Nodes) == 0 && graph.NodeCount() > 0
}
