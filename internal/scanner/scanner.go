package scanner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/olgasafonova/ridge/internal/model"
)

// ErrLimitReached is returned alongside a partial graph when a scan limit is hit.
var ErrLimitReached = errors.New("scan limit reached")

// ScanOptions controls scan behavior.
type ScanOptions struct {
	MaxFiles     int           // 0 = unlimited
	MaxNodes     int           // 0 = unlimited
	Timeout      time.Duration // 0 = no timeout
	SkipDirs     []string      // additional dirs to skip (merged with defaults)
	SkipGlobs    []string      // filepath.Match patterns to skip (e.g. "*_test.go")
	IncludeTests bool          // if true, don't skip test files (default: skip them)
	Workers      int           // 0 = runtime.NumCPU(), capped at 8
	State        *ScanState    // non-nil enables incremental scanning (reuse cached results for unchanged files)
}

// defaultTestGlobs are file patterns excluded by default to avoid test fixtures
// polluting the architecture graph. Set IncludeTests=true to override.
var defaultTestGlobs = []string{
	"*_test.go",
	"*.test.ts",
	"*.test.tsx",
	"*.spec.ts",
	"*.spec.tsx",
	"*.test.js",
	"*.spec.js",
	"test_*.py",
	"*_test.py",
	"conftest.py",
}

// DefaultScanOptions returns permissive defaults with no limits.
func DefaultScanOptions() ScanOptions {
	return ScanOptions{}
}

// ScanResult holds the graph and statistics from a scan.
type ScanResult struct {
	Graph     *model.ArchGraph
	Stats     ScanStats
	Truncated bool       // true if any limit was hit
	State     *ScanState // updated state after scan (for persistence)
}

// ScanStats contains metrics about the scan.
type ScanStats struct {
	FilesAnalyzed    int   `json:"files_analyzed"`
	FilesSkipped     int   `json:"files_skipped"`
	FilesCached      int   `json:"files_cached,omitempty"`      // files reused from cache
	FilesChanged     int   `json:"files_changed,omitempty"`     // files re-analyzed due to changes
	FilesInvalidated int   `json:"files_invalidated,omitempty"` // files re-analyzed due to analyzer signature change
	NodesFound       int   `json:"nodes_found"`
	EdgesFound       int   `json:"edges_found"`
	DurationMs       int64 `json:"duration_ms"`
}

// Scanner walks a codebase directory and delegates files to registered analyzers.
type Scanner struct {
	analyzers map[string]Analyzer // extension -> analyzer
	logger    *slog.Logger
	skipDirs  map[string]bool
}

// New creates a Scanner with the given analyzers.
func New(logger *slog.Logger, analyzers ...Analyzer) *Scanner {
	extMap := make(map[string]Analyzer)
	for _, a := range analyzers {
		for _, ext := range a.Extensions() {
			extMap[ext] = a
		}
	}

	return &Scanner{
		analyzers: extMap,
		logger:    logger,
		skipDirs: map[string]bool{
			"node_modules": true,
			".git":         true,
			"vendor":       true,
			"dist":         true,
			"build":        true,
			"__pycache__":  true,
			".venv":        true,
			"venv":         true,
			".next":        true,
			".nuxt":        true,
			"target":       true, // Rust/Java build output
		},
	}
}

// Scan walks the directory tree and returns an ArchGraph. Backwards-compatible wrapper.
func (s *Scanner) Scan(rootPath string) (*model.ArchGraph, error) {
	result, err := s.ScanWithOptions(context.Background(), rootPath, DefaultScanOptions())
	if err != nil && !errors.Is(err, ErrLimitReached) {
		return nil, err
	}
	return result.Graph, nil
}

// ScanWithOptions walks the directory tree with configurable limits, timeout, and skip patterns.
// Uses a three-phase approach: collect files, detect changes (if incremental), then analyze.
func (s *Scanner) ScanWithOptions(ctx context.Context, rootPath string, opts ScanOptions) (*ScanResult, error) {
	start := time.Now()

	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	mergedSkipDirs := s.mergeSkipDirs(opts.SkipDirs)
	skipGlobs := opts.SkipGlobs
	if !opts.IncludeTests {
		skipGlobs = append(skipGlobs, defaultTestGlobs...)
	}

	var stats ScanStats
	files, walkSkipped, truncated, err := s.walkAndCollect(ctx, walkParams{
		absRoot:   absRoot,
		skipDirs:  mergedSkipDirs,
		skipGlobs: skipGlobs,
		maxFiles:  opts.MaxFiles,
	})
	if err != nil {
		return nil, fmt.Errorf("walking directory: %w", err)
	}
	stats.FilesSkipped = walkSkipped

	state := opts.State
	toAnalyze, cachedResults := s.partitionForIncremental(files, state, &stats)

	graph := model.NewGraph(absRoot)
	mergeCachedResults(graph, cachedResults, &stats)

	workers := chooseWorkers(opts.Workers, len(toAnalyze))
	ac := &analyzeContext{
		state:    state,
		graph:    graph,
		stats:    &stats,
		maxNodes: opts.MaxNodes,
		sigByExt: s.signatureByExt(),
	}
	if analyzeTruncated := s.runAnalysis(ctx, toAnalyze, workers, ac); analyzeTruncated {
		truncated = true
	}

	if state != nil {
		state.LastScan = time.Now()
	}
	stats.DurationMs = time.Since(start).Milliseconds()

	if ctx.Err() != nil {
		truncated = true
	}

	// Stamp Source on every node so consumers can identify which scan root
	// produced it. This is especially useful when multiple graphs are merged.
	stampSource(graph, absRoot)

	s.logger.Info("Scan complete",
		"root", absRoot,
		"files_analyzed", stats.FilesAnalyzed,
		"files_cached", stats.FilesCached,
		"nodes", stats.NodesFound,
		"edges", stats.EdgesFound,
		"workers", workers,
		"truncated", truncated,
		"duration_ms", stats.DurationMs,
	)

	result := &ScanResult{Graph: graph, Stats: stats, Truncated: truncated, State: state}
	if truncated {
		return result, ErrLimitReached
	}
	return result, nil
}

// SupportedExtensions returns all file extensions the scanner handles.
func (s *Scanner) SupportedExtensions() []string {
	exts := make([]string, 0, len(s.analyzers))
	for ext := range s.analyzers {
		exts = append(exts, ext)
	}
	return exts
}

// stampSource sets Node.Source to scanRoot for every node that has no Source yet.
// Called after ScanWithOptions assembles the graph, so single-path scans tag all
// nodes with their root, and multi-path merges preserve the first-writer's Source.
func stampSource(g *model.ArchGraph, scanRoot string) {
	for _, n := range g.Nodes() {
		if n.Source == "" {
			n.Source = scanRoot
		}
	}
}
