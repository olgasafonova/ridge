package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/olgasafonova/ridge/internal/detector"
	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/safepath"
)

// =============================================================================
// arch_dependencies
// =============================================================================

// ArchDependenciesArgs are the arguments for arch_dependencies.
type ArchDependenciesArgs struct {
	Path string `json:"path"`
	Repo string `json:"repo,omitempty"`
	ScanControl
}

// ArchDependenciesResult is the result of arch_dependencies.
type ArchDependenciesResult struct {
	Internal       []string `json:"internal"`
	External       []string `json:"external"`
	Infrastructure []string `json:"infrastructure"`
}

func (h *HandlerRegistry) archDependencies(ctx context.Context, args ArchDependenciesArgs) (*ArchDependenciesResult, error) {
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

	external, infra := collectExternalAndInfra(graph, loadModule(path))
	internal := collectInternal(graph)

	return &ArchDependenciesResult{
		Internal:       ensureSlice(internal),
		External:       ensureSlice(external),
		Infrastructure: ensureSlice(infra),
	}, nil
}

// dependencyKind classifies a dependency edge into one of three buckets.
type dependencyKind int

const (
	depSkip dependencyKind = iota
	depExternal
	depInfra
)

// classifyDependency returns the bucket for a single dependency edge plus the
// label to record. depSkip means the edge contributes nothing (wrong type or
// stdlib). Pulled out of archDependencies to flatten the loop body.
func classifyDependency(e *model.Edge, graph *model.ArchGraph, mod Module) (string, dependencyKind) {
	if e.Type != model.EdgeDependency {
		return "", depSkip
	}
	target := e.Label
	if target == "" {
		target = e.Target
	}

	if node := graph.GetNode(e.Target); node != nil {
		switch node.Type {
		case model.NodeDatabase, model.NodeQueue, model.NodeCache:
			return target, depInfra
		}
	}

	if mod.IsStdlib(target) {
		return "", depSkip
	}
	return target, depExternal
}

// collectExternalAndInfra walks dependency edges once, deduplicating by label
// and bucketing into external vs infrastructure.
func collectExternalAndInfra(graph *model.ArchGraph, mod Module) (external, infra []string) {
	seen := make(map[string]bool)
	for _, e := range graph.Edges() {
		label, kind := classifyDependency(e, graph, mod)
		if kind == depSkip || seen[label] {
			continue
		}
		seen[label] = true
		switch kind {
		case depInfra:
			infra = append(infra, label)
		case depExternal:
			external = append(external, label)
		}
	}
	return external, infra
}

// collectInternal returns the names of all package nodes in the graph.
func collectInternal(graph *model.ArchGraph) []string {
	pkgs := graph.NodesByType(model.NodePackage)
	out := make([]string, 0, len(pkgs))
	for _, n := range pkgs {
		out = append(out, n.Name)
	}
	return out
}

// ensureSlice replaces a nil string slice with an empty one so JSON serializes
// as [] instead of null. Used by handlers that expose categorized result lists.
func ensureSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// =============================================================================
// arch_blast_radius
// =============================================================================

// ArchBlastRadiusArgs are the arguments for arch_blast_radius.
type ArchBlastRadiusArgs struct {
	Path     string `json:"path"`
	Repo     string `json:"repo,omitempty"`
	Target   string `json:"target"`
	MaxDepth int    `json:"max_depth,omitempty"`
	ScanControl
}

// ArchBlastRadiusResult is the result of arch_blast_radius.
type ArchBlastRadiusResult struct {
	Target      string                      `json:"target"`
	TargetID    string                      `json:"target_id"`
	Direct      int                         `json:"direct"`
	Total       int                         `json:"total"`
	MaxDepthHit bool                        `json:"max_depth_hit"`
	Dependents  []detector.BlastRadiusEntry `json:"dependents"`
}

func (h *HandlerRegistry) archBlastRadius(ctx context.Context, args ArchBlastRadiusArgs) (*ArchBlastRadiusResult, error) {
	if args.Target == "" {
		return nil, fmt.Errorf("target is required")
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

	targetID, ok := detector.ResolveTargetToID(graph, args.Target)
	if !ok {
		return nil, fmt.Errorf("target %q not found in scanned graph (run arch_scan to list available node IDs and paths)", args.Target)
	}

	res := detector.ComputeBlastRadius(graph, targetID, args.MaxDepth)
	if res.Dependents == nil {
		res.Dependents = []detector.BlastRadiusEntry{}
	}

	return &ArchBlastRadiusResult{
		Target:      args.Target,
		TargetID:    res.TargetID,
		Direct:      res.Direct,
		Total:       res.Total,
		MaxDepthHit: res.MaxDepthHit,
		Dependents:  res.Dependents,
	}, nil
}

// =============================================================================
// Module path / stdlib helpers
// =============================================================================

// Module wraps the module declaration loaded from a project's go.mod and owns
// the rules for classifying an import path as internal vs stdlib vs external.
type Module struct {
	Path string
}

// loadModule reads go.mod from dir and returns a Module. An empty Module
// (Path: "") means go.mod was missing or unparseable; classification still
// works (everything not-stdlib looks external).
func loadModule(dir string) Module {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return Module{}
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return Module{Path: strings.TrimSpace(strings.TrimPrefix(line, "module "))}
		}
	}
	return Module{}
}

// IsStdlib reports whether importPath belongs to the Go standard library or
// the extended stdlib (golang.org/x/*). Imports that belong to this Module
// are internal, not stdlib.
func (m Module) IsStdlib(importPath string) bool {
	if m.Path != "" && strings.HasPrefix(importPath, m.Path) {
		return false
	}
	// Go stdlib packages don't contain dots in the first path segment.
	first, _, _ := strings.Cut(importPath, "/")
	if !strings.Contains(first, ".") {
		return true
	}
	return strings.HasPrefix(importPath, "golang.org/x/")
}
