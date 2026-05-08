package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sync/errgroup"

	"github.com/olgasafonova/ridge/internal/detector"
	"github.com/olgasafonova/ridge/internal/drift"
	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/safepath"
)

// =============================================================================
// arch_validate
// =============================================================================

type ArchValidateArgs struct {
	Path string `json:"path"`
	Repo string `json:"repo,omitempty"`
	ScanControl
}

type ArchValidateResult struct {
	Valid      bool     `json:"valid"`
	Violations []string `json:"violations"`
	Summary    string   `json:"summary"`
}

func (h *HandlerRegistry) archValidate(ctx context.Context, args ArchValidateArgs) (*ArchValidateResult, error) {
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

	// Load custom rules if .arch-rules.yaml exists in the project
	var customRules *detector.RulesConfig
	rulesPath := filepath.Join(path, ".arch-rules.yaml")
	if _, err := os.Stat(rulesPath); err == nil {
		customRules, err = detector.LoadRules(rulesPath)
		if err != nil {
			h.logger.Warn("Failed to load custom rules", "path", rulesPath, "error", err)
		}
	}

	detectedViolations := detector.ValidateGraph(graph, customRules)
	violations := make([]string, 0, len(detectedViolations))
	for _, v := range detectedViolations {
		violations = append(violations, fmt.Sprintf("[%s] %s: %s", v.Severity, v.Rule, v.Detail))
	}

	return &ArchValidateResult{
		Valid:      len(violations) == 0,
		Violations: violations,
		Summary:    fmt.Sprintf("Validation complete: %d violations found", len(violations)),
	}, nil
}

// =============================================================================
// arch_history
// =============================================================================

type ArchHistoryArgs struct {
	Path  string `json:"path"`
	Repo  string `json:"repo,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type ArchHistoryResult struct {
	Entries []drift.HistoryEntry `json:"entries"`
	Summary string               `json:"summary"`
}

// scanResult holds the output of scanning a single commit in parallel.
type scanResult struct {
	graph *model.ArchGraph
	entry drift.HistoryEntry
}

func (h *HandlerRegistry) archHistory(ctx context.Context, args ArchHistoryArgs) (*ArchHistoryResult, error) {
	path, _, err := h.resolveRepoPath(args.Path, args.Repo)
	if err != nil {
		return nil, err
	}
	if err := safepath.ValidateScanPath(path); err != nil {
		return nil, err
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	commits, err := drift.GetSignificantCommits(ctx, path, limit)
	if err != nil {
		return nil, fmt.Errorf("getting git history: %w", err)
	}

	// Phase 1: Parallel scan — each goroutine writes only to its own index.
	results := make([]scanResult, len(commits))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4) // cap concurrent git worktrees

	for idx, c := range commits {
		results[idx].entry = drift.HistoryEntry{
			Ref:     c.Hash[:8],
			Date:    c.Date,
			Message: c.Message,
		}
		g.Go(func() error {
			worktree, cleanup, wErr := drift.CheckoutRef(gctx, path, c.Hash)
			if wErr != nil {
				return nil // non-fatal: entry stays with zero counts
			}
			graph, scanErr := h.scanner.Scan(worktree)
			cleanup()
			if scanErr != nil {
				return nil // non-fatal
			}
			results[idx].graph = graph
			results[idx].entry.NodeCount = graph.NodeCount()
			results[idx].entry.EdgeCount = graph.EdgeCount()
			results[idx].entry.Topology = string(graph.Topology)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("scanning commits: %w", err)
	}

	// Phase 2: Sequential comparison (oldest-first).
	// commits[0] is most recent; walk from end to start.
	var prevGraph *model.ArchGraph
	for i := len(results) - 1; i >= 0; i-- {
		r := &results[i]
		if r.graph != nil && prevGraph != nil {
			report := drift.Compare(prevGraph, r.graph)
			r.entry.ChangesFromPrevious = len(report.Changes)
		}
		if r.graph != nil {
			prevGraph = r.graph
		}
	}

	// Collect entries in most-recent-first order (same order as commits).
	entries := make([]drift.HistoryEntry, len(results))
	for i, r := range results {
		entries[i] = r.entry
	}

	return &ArchHistoryResult{
		Entries: entries,
		Summary: fmt.Sprintf("Analyzed %d commits", len(entries)),
	}, nil
}
