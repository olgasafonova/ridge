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
	for _, spec := range AllTools {
		switch spec.Method {
		case "ArchScan":
			register(h, server, spec, h.archScan)
		case "ArchFocus":
			register(h, server, spec, h.archFocus)
		case "ArchGenerate":
			register(h, server, spec, h.archGenerate)
		case "ArchDependencies":
			register(h, server, spec, h.archDependencies)
		case "ArchBlastRadius":
			register(h, server, spec, h.archBlastRadius)
		case "ArchDataflow":
			register(h, server, spec, h.archDataflow)
		case "ArchBoundaries":
			register(h, server, spec, h.archBoundaries)
		case "ArchDiff":
			register(h, server, spec, h.archDiff)
		case "ArchDrift":
			register(h, server, spec, h.archDrift)
		case "ArchDriftExplain":
			register(h, server, spec, h.archDriftExplain)
		case "ArchValidate":
			register(h, server, spec, h.archValidate)
		case "ArchHistory":
			register(h, server, spec, h.archHistory)
		case "ArchSnapshot":
			register(h, server, spec, h.archSnapshot)
		case "ArchMetrics":
			register(h, server, spec, h.archMetrics)
		case "ArchExplain":
			register(h, server, spec, h.archExplain)
		case "ArchRecommend":
			register(h, server, spec, h.archRecommend)
		case "ArchRegistryAdd":
			register(h, server, spec, h.archRegistryAdd)
		case "ArchRegistryRemove":
			register(h, server, spec, h.archRegistryRemove)
		case "ArchRegistryList":
			register(h, server, spec, h.archRegistryList)
		}
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
	MaxFiles    int      `json:"max_files,omitempty"`
	MaxNodes    int      `json:"max_nodes,omitempty"`
	TimeoutSecs int      `json:"timeout_secs,omitempty"`
	SkipDirs    []string `json:"skip_dirs,omitempty"`
	SkipGlobs   []string `json:"skip_globs,omitempty"`
	Workers     int      `json:"workers,omitempty"`
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
