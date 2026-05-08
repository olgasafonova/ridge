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

	modulePath := readModulePath(path)

	var internal, external, infra []string
	seen := make(map[string]bool)

	for _, e := range graph.Edges() {
		if e.Type != model.EdgeDependency {
			continue
		}
		target := e.Label
		if target == "" {
			target = e.Target
		}
		if seen[target] {
			continue
		}
		seen[target] = true

		// Classify: internal (starts with module path), external, infra
		node := graph.GetNode(e.Target)
		if node != nil {
			switch node.Type {
			case model.NodeDatabase, model.NodeQueue, model.NodeCache:
				infra = append(infra, target)
				continue
			}
		}

		if isStdlib(target, modulePath) {
			continue // skip stdlib
		}
		external = append(external, target)
	}

	// Internal: package nodes
	for _, n := range graph.NodesByType(model.NodePackage) {
		internal = append(internal, n.Name)
	}

	// Ensure non-nil slices so JSON serializes as [] not null.
	if internal == nil {
		internal = []string{}
	}
	if external == nil {
		external = []string{}
	}
	if infra == nil {
		infra = []string{}
	}

	return &ArchDependenciesResult{
		Internal:       internal,
		External:       external,
		Infrastructure: infra,
	}, nil
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

// readModulePath extracts the module declaration from go.mod in the given directory.
// Returns "" if go.mod doesn't exist or can't be parsed.
func readModulePath(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func isStdlib(importPath, modulePath string) bool {
	// If it belongs to the scanned module, it's internal — not stdlib
	if modulePath != "" && strings.HasPrefix(importPath, modulePath) {
		return false
	}
	// Go stdlib packages don't contain dots in the first path segment.
	// Extended stdlib (golang.org/x/*) also excluded from external deps.
	first, _, _ := strings.Cut(importPath, "/")
	if !strings.Contains(first, ".") {
		return true
	}
	return strings.HasPrefix(importPath, "golang.org/x/")
}
