package tools

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olgasafonova/ridge/internal/analyzer/golang"
	"github.com/olgasafonova/ridge/internal/analyzer/java"
	"github.com/olgasafonova/ridge/internal/analyzer/markdown"
	"github.com/olgasafonova/ridge/internal/analyzer/python"
	"github.com/olgasafonova/ridge/internal/analyzer/rust"
	"github.com/olgasafonova/ridge/internal/analyzer/typescript"
	"github.com/olgasafonova/ridge/internal/infra"
	"github.com/olgasafonova/ridge/internal/registry"
	"github.com/olgasafonova/ridge/internal/scanner"
)

// HandlerRegistry holds the state and dependencies for all tool handlers.
type HandlerRegistry struct {
	scanner      *scanner.Scanner
	cache        *infra.Cache[*scanner.ScanResult]
	repoRegistry *registry.Registry
	logger       *slog.Logger
}

// NewHandlerRegistry creates a registry with all dependencies wired.
func NewHandlerRegistry(logger *slog.Logger) *HandlerRegistry {
	goAnalyzer := golang.New()
	tsAnalyzer := typescript.New()
	pyAnalyzer := python.New()
	mdAnalyzer := markdown.New()
	rustAnalyzer := rust.New()
	javaAnalyzer := java.New()
	s := scanner.New(logger, goAnalyzer, tsAnalyzer, pyAnalyzer, mdAnalyzer, rustAnalyzer, javaAnalyzer)

	reg, err := registry.Load()
	if err != nil {
		logger.Warn("Failed to load repo registry, starting empty", "error", err)
		reg = nil
	}

	return &HandlerRegistry{
		scanner:      s,
		cache:        infra.NewCache[*scanner.ScanResult](5*time.Minute, 10),
		repoRegistry: reg,
		logger:       logger,
	}
}

// RegisterAll registers all tools with the MCP server.
func (h *HandlerRegistry) RegisterAll(server *mcp.Server) {
	registrars := h.registrars()
	for _, spec := range AllTools {
		if registerTool, ok := registrars[spec.Method]; ok {
			registerTool(server, spec)
		}
	}
}

// registrars maps each ToolSpec.Method name to a closure that performs the
// typed generic registration for that handler. The closures exist because
// register is generic over per-handler Args/Result types, which rules out a
// homogeneous method-value table.
func (h *HandlerRegistry) registrars() map[string]func(*mcp.Server, ToolSpec) {
	return map[string]func(*mcp.Server, ToolSpec){
		"ArchScan":           func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archScan) },
		"ArchFocus":          func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archFocus) },
		"ArchGenerate":       func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archGenerate) },
		"ArchDependencies":   func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archDependencies) },
		"ArchBlastRadius":    func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archBlastRadius) },
		"ArchDataflow":       func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archDataflow) },
		"ArchBoundaries":     func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archBoundaries) },
		"ArchDiff":           func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archDiff) },
		"ArchDrift":          func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archDrift) },
		"ArchDriftExplain":   func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archDriftExplain) },
		"ArchValidate":       func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archValidate) },
		"ArchHistory":        func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archHistory) },
		"ArchSnapshot":       func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archSnapshot) },
		"ArchMetrics":        func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archMetrics) },
		"ArchExplain":        func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archExplain) },
		"ArchRecommend":      func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archRecommend) },
		"ArchRegistryAdd":    func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archRegistryAdd) },
		"ArchRegistryRemove": func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archRegistryRemove) },
		"ArchRegistryList":   func(s *mcp.Server, spec ToolSpec) { register(h, s, spec, h.archRegistryList) },
	}
}

// RegisteredTools returns the tool specs for introspection.
func (h *HandlerRegistry) RegisteredTools() []ToolSpec {
	return AllTools
}

// =============================================================================
// ScanControl — embedded in Args structs that trigger scans
// =============================================================================

// ScanControl contains optional fields to control scan behavior.
type ScanControl struct {
	MaxFiles    int      `json:"max_files,omitempty" jsonschema:"Stop analyzing after this many files. Use on a large repo to bound scan time; the result is marked truncated when a limit is hit. Unset means no limit."`
	MaxNodes    int      `json:"max_nodes,omitempty" jsonschema:"Stop adding nodes to the graph after this many. Bounds output size on a large repo; the result is marked truncated when a limit is hit. Unset means no limit."`
	TimeoutSecs int      `json:"timeout_secs,omitempty" jsonschema:"Abort the scan after this many seconds. Defaults to 120 when unset."`
	SkipDirs    []string `json:"skip_dirs,omitempty" jsonschema:"Directory names to skip, matched against each path segment (e.g. vendor, node_modules, testdata)."`
	SkipGlobs   []string `json:"skip_globs,omitempty" jsonschema:"Glob patterns matched against file paths; matching files are skipped (e.g. **/*_test.go, **/generated/**)."`
	Workers     int      `json:"workers,omitempty" jsonschema:"Number of parallel analyzer workers. Capped at 32. Unset lets the scanner choose."`
}

func (sc ScanControl) toScanOptions() scanner.ScanOptions {
	opts := scanner.DefaultScanOptions()
	if sc.MaxFiles > 0 {
		opts.MaxFiles = sc.MaxFiles
	}
	if sc.MaxNodes > 0 {
		opts.MaxNodes = sc.MaxNodes
	}
	if sc.TimeoutSecs > 0 {
		opts.Timeout = time.Duration(sc.TimeoutSecs) * time.Second
	} else {
		opts.Timeout = 120 * time.Second
	}
	if sc.Workers > 0 {
		opts.Workers = min(sc.Workers, 32)
	}
	opts.SkipDirs = sc.SkipDirs
	opts.SkipGlobs = sc.SkipGlobs
	return opts
}

// resolveRepoPath resolves a path from either an explicit path or a registry alias.
// Path takes precedence over repo. Returns the resolved path, alias (empty for ad-hoc), and error.
func (h *HandlerRegistry) resolveRepoPath(path, repo string) (string, string, error) {
	if path != "" {
		return path, "", nil
	}
	if repo == "" {
		return "", "", fmt.Errorf("either path or repo is required")
	}
	if h.repoRegistry == nil {
		return "", "", fmt.Errorf("repo registry not available")
	}
	entry, err := h.repoRegistry.Get(repo)
	if err != nil {
		return "", "", err
	}
	return entry.Path, repo, nil
}

// =============================================================================
// Generic registration helper
// =============================================================================

func register[Args, Result any](
	h *HandlerRegistry,
	server *mcp.Server,
	spec ToolSpec,
	handler func(context.Context, Args) (Result, error),
) {
	tool := &mcp.Tool{
		Name:        spec.Name,
		Description: spec.Description,
		Annotations: &mcp.ToolAnnotations{
			Title:          spec.Title,
			ReadOnlyHint:   spec.ReadOnly,
			IdempotentHint: spec.Idempotent,
		},
	}
	if spec.OpenWorld {
		tool.Annotations.OpenWorldHint = ptr(true)
	}

	mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, args Args) (callResult *mcp.CallToolResult, result Result, retErr error) {
		defer h.recoverPanic(spec.Name, &retErr)

		res, err := handler(ctx, args)
		if err != nil {
			var zero Result
			return nil, zero, fmt.Errorf("%s failed: %w", spec.Name, err)
		}
		return nil, res, nil
	})
}
