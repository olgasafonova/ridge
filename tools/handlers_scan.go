package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/olgasafonova/ridge/internal/detector"
	"github.com/olgasafonova/ridge/internal/infra"
	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/safepath"
	"github.com/olgasafonova/ridge/internal/scanner"
)

// =============================================================================
// Scan helpers
// =============================================================================

// cachedScan checks the cache before running a full scan.
// When alias is non-empty, loads/saves incremental scan state and updates registry metadata.
// Returns the result even on ErrLimitReached (partial result).
func (h *HandlerRegistry) cachedScan(ctx context.Context, path, alias string, opts scanner.ScanOptions) (*scanner.ScanResult, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	key := infra.CacheKey(absPath, scanOptsKey(opts))
	if cached, ok := h.cache.Get(key); ok {
		h.logger.Debug("Scan cache hit", "path", absPath)
		return cached, nil
	}

	if alias != "" {
		opts.State = h.loadIncrementalState(alias, absPath)
	}

	result, err := h.scanner.ScanWithOptions(ctx, path, opts)
	if err != nil && !errors.Is(err, scanner.ErrLimitReached) {
		return nil, err
	}

	h.cache.Put(key, result)

	if alias != "" && result.State != nil {
		h.persistIncrementalState(alias, result)
	}

	return result, err
}

// scanOptsKey builds a deterministic cache-key suffix from the limit and
// skip-pattern fields of ScanOptions.
func scanOptsKey(opts scanner.ScanOptions) string {
	return fmt.Sprintf("%d|%d|%d|%s|%s",
		opts.MaxFiles, opts.MaxNodes, opts.Workers,
		strings.Join(opts.SkipDirs, ","),
		strings.Join(opts.SkipGlobs, ","),
	)
}

// loadIncrementalState reads persistent scan state for an alias, falling back
// to a fresh state when none exists or loading fails.
func (h *HandlerRegistry) loadIncrementalState(alias, absPath string) *scanner.ScanState {
	if h.repoRegistry == nil {
		return nil
	}
	statePath := h.repoRegistry.StatePath(alias)
	state, loadErr := infra.LoadJSON[scanner.ScanState](statePath)
	if loadErr != nil {
		if !os.IsNotExist(loadErr) {
			h.logger.Warn("Failed to load scan state, starting fresh", "alias", alias, "error", loadErr)
		}
		return scanner.NewScanState(absPath)
	}
	return state
}

// persistIncrementalState writes the post-scan state and refreshes the registry
// row for the alias. Failures are logged but non-fatal.
func (h *HandlerRegistry) persistIncrementalState(alias string, result *scanner.ScanResult) {
	if h.repoRegistry == nil {
		return
	}
	statePath := h.repoRegistry.StatePath(alias)
	if saveErr := infra.SaveJSON(statePath, result.State); saveErr != nil {
		h.logger.Warn("Failed to save scan state", "alias", alias, "error", saveErr)
	}
	h.repoRegistry.UpdateScanInfo(alias, result.Graph.NodeCount(), result.Graph.EdgeCount(), string(result.Graph.Topology))
	if saveErr := h.repoRegistry.Save(); saveErr != nil {
		h.logger.Warn("Failed to save registry", "error", saveErr)
	}
}

// scanPath runs a cached scan and unwraps the graph.
// Returns the graph even on ErrLimitReached (partial result).
func (h *HandlerRegistry) scanPath(ctx context.Context, path, alias string, sc ScanControl) (*model.ArchGraph, bool, error) {
	result, err := h.cachedScan(ctx, path, alias, sc.toScanOptions())
	if err != nil && !errors.Is(err, scanner.ErrLimitReached) {
		return nil, false, err
	}
	return result.Graph, result.Truncated, nil
}

// =============================================================================
// arch_scan
// =============================================================================

// ArchScanArgs are the arguments for arch_scan.
type ArchScanArgs struct {
	Path        string   `json:"path"`
	Paths       []string `json:"paths,omitempty"`
	Repo        string   `json:"repo,omitempty"`
	Detail      string   `json:"detail,omitempty"`       // "summary" (default) or "full"
	Samples     bool     `json:"samples,omitempty"`      // when true, populate Node.Source with N source lines for file-backed nodes
	SampleLines int      `json:"sample_lines,omitempty"` // lines per sample (default 6); requires samples=true
	ScanControl
}

// ArchScanResult is the result of arch_scan.
type ArchScanResult struct {
	RootPath  string             `json:"root_path"`
	Topology  string             `json:"topology"`
	NodeCount int                `json:"node_count"`
	EdgeCount int                `json:"edge_count"`
	Nodes     []*model.Node      `json:"nodes,omitempty"`
	Edges     []*model.Edge      `json:"edges,omitempty"`
	Summary   string             `json:"summary"`
	Stats     *scanner.ScanStats `json:"stats,omitempty"`
	Truncated bool               `json:"truncated,omitempty"`
}

func (h *HandlerRegistry) archScan(ctx context.Context, args ArchScanArgs) (*ArchScanResult, error) {
	if err := validateScanInputs(args); err != nil {
		return nil, err
	}
	if len(args.Paths) > 0 {
		return h.archScanMulti(ctx, args)
	}
	return h.archScanSingle(ctx, args)
}

// validateScanInputs enforces the mutual exclusion between args.Paths and
// args.Path/args.Repo: a single arch_scan call must use one shape or the other.
func validateScanInputs(args ArchScanArgs) error {
	if len(args.Paths) == 0 {
		return nil
	}
	if args.Path != "" || args.Repo != "" {
		return fmt.Errorf("use either path or paths, not both")
	}
	return nil
}

func (h *HandlerRegistry) archScanSingle(ctx context.Context, args ArchScanArgs) (*ArchScanResult, error) {
	path, alias, err := h.resolveRepoPath(args.Path, args.Repo)
	if err != nil {
		return nil, err
	}
	if err := safepath.ValidateScanPath(path); err != nil {
		return nil, err
	}

	result, err := h.cachedScan(ctx, path, alias, args.toScanOptions())
	if err != nil && !errors.Is(err, scanner.ErrLimitReached) {
		return nil, fmt.Errorf("scanning codebase: %w", err)
	}
	graph := result.Graph

	// Detect topology from project structure
	boundaries, bErr := detector.DetectBoundaries(path)
	if bErr == nil {
		graph.Topology = boundaries.Topology
	}

	res := &ArchScanResult{
		RootPath:  graph.RootPath,
		Topology:  string(graph.Topology),
		NodeCount: graph.NodeCount(),
		EdgeCount: graph.EdgeCount(),
		Summary:   graph.Summary(),
		Stats:     &result.Stats,
		Truncated: result.Truncated,
	}

	if strings.EqualFold(args.Detail, "full") {
		if args.Samples {
			scanner.PopulateSamples(graph, args.SampleLines)
		}
		graph.RelativePaths()
		res.Nodes = graph.Nodes()
		res.Edges = graph.Edges()
	}

	return res, nil
}

// archScanMulti scans each path in args.Paths independently and merges results
// into a single graph. Each node carries a Source field recording which scan
// root produced it. The merged graph uses the first path as RootPath.
func (h *HandlerRegistry) archScanMulti(ctx context.Context, args ArchScanArgs) (*ArchScanResult, error) {
	if len(args.Paths) == 0 {
		return nil, fmt.Errorf("paths must not be empty")
	}

	opts := args.toScanOptions()
	var (
		merged     *model.ArchGraph
		truncated  bool
		totalStats scanner.ScanStats
	)

	for _, p := range args.Paths {
		if err := safepath.ValidateScanPath(p); err != nil {
			return nil, fmt.Errorf("invalid path %q: %w", p, err)
		}

		result, err := h.cachedScan(ctx, p, "", opts)
		if err != nil && !errors.Is(err, scanner.ErrLimitReached) {
			return nil, fmt.Errorf("scanning %q: %w", p, err)
		}
		truncated = truncated || result.Truncated
		addScanStats(&totalStats, result.Stats)
		merged = mergeOrAdopt(merged, result.Graph)
	}

	res := &ArchScanResult{
		RootPath:  merged.RootPath,
		Topology:  string(merged.Topology),
		NodeCount: merged.NodeCount(),
		EdgeCount: merged.EdgeCount(),
		Summary:   merged.Summary(),
		Stats:     &totalStats,
		Truncated: truncated,
	}

	if strings.EqualFold(args.Detail, "full") {
		if args.Samples {
			scanner.PopulateSamples(merged, args.SampleLines)
		}
		res.Nodes = merged.Nodes()
		res.Edges = merged.Edges()
	}

	return res, nil
}

// =============================================================================
// arch_focus
// =============================================================================

// ArchFocusArgs are the arguments for arch_focus.
type ArchFocusArgs struct {
	Path string `json:"path"`
	Repo string `json:"repo,omitempty"`
	ScanControl
}

func (h *HandlerRegistry) archFocus(ctx context.Context, args ArchFocusArgs) (*ArchScanResult, error) {
	return h.archScan(ctx, ArchScanArgs{Path: args.Path, Repo: args.Repo, ScanControl: args.ScanControl})
}

// addScanStats accumulates a partial ScanStats into a running total.
func addScanStats(total *scanner.ScanStats, partial scanner.ScanStats) {
	total.FilesAnalyzed += partial.FilesAnalyzed
	total.FilesSkipped += partial.FilesSkipped
	total.NodesFound += partial.NodesFound
	total.EdgesFound += partial.EdgesFound
	total.DurationMs += partial.DurationMs
}

// mergeOrAdopt returns next when accumulator is nil, otherwise merges next into
// accumulator and returns the merged graph.
func mergeOrAdopt(accumulator, next *model.ArchGraph) *model.ArchGraph {
	if accumulator == nil {
		return next
	}
	accumulator.Merge(next)
	return accumulator
}
