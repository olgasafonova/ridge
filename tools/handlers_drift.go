package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/olgasafonova/ridge/internal/drift"
	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/safepath"
	"github.com/olgasafonova/ridge/internal/scanner"
)

// =============================================================================
// arch_diff (compare against snapshot file)
// =============================================================================

type ArchDiffArgs struct {
	Path         string `json:"path"`
	Repo         string `json:"repo,omitempty"`
	SnapshotFile string `json:"snapshot_file"`
}

func (h *HandlerRegistry) archDiff(ctx context.Context, args ArchDiffArgs) (*model.DiffReport, error) {
	path, alias, err := h.resolveRepoPath(args.Path, args.Repo)
	if err != nil {
		return nil, err
	}
	if err := safepath.ValidateScanPath(path); err != nil {
		return nil, err
	}
	if args.SnapshotFile == "" {
		return nil, fmt.Errorf("snapshot_file is required")
	}
	if err := safepath.ValidateOutputPath(args.SnapshotFile, path); err != nil {
		return nil, fmt.Errorf("invalid snapshot file path: %w", err)
	}

	// Load baseline from snapshot
	snapshot, err := drift.Load(args.SnapshotFile)
	if err != nil {
		return nil, fmt.Errorf("loading snapshot: %w", err)
	}
	baseline := snapshot.ToGraph()

	// Scan current codebase
	result, scanErr := h.cachedScan(ctx, path, alias, scanner.DefaultScanOptions())
	if scanErr != nil && !errors.Is(scanErr, scanner.ErrLimitReached) {
		return nil, fmt.Errorf("scanning current codebase: %w", scanErr)
	}

	report := drift.Compare(baseline, result.Graph)
	report.BaseRef = args.SnapshotFile
	report.CompareRef = "current"
	return report, nil
}

// =============================================================================
// arch_drift (compare two git refs)
// =============================================================================

type ArchDriftArgs struct {
	Path    string `json:"path"`
	Repo    string `json:"repo,omitempty"`
	BaseRef string `json:"base_ref"`
	HeadRef string `json:"head_ref,omitempty"`
}

func (h *HandlerRegistry) archDrift(ctx context.Context, args ArchDriftArgs) (*model.DiffReport, error) {
	path, _, err := h.resolveRepoPath(args.Path, args.Repo)
	if err != nil {
		return nil, err
	}
	if err := safepath.ValidateScanPath(path); err != nil {
		return nil, err
	}
	if args.BaseRef == "" {
		return nil, fmt.Errorf("base_ref is required")
	}
	headRef := args.HeadRef
	if headRef == "" {
		headRef = "HEAD"
	}

	baseGraph, baseCleanup, err := h.checkoutAndScan(ctx, path, args.BaseRef, "base")
	if err != nil {
		return nil, err
	}
	defer baseCleanup()

	headGraph, headCleanup, err := h.checkoutAndScan(ctx, path, headRef, "head")
	if err != nil {
		return nil, err
	}
	defer headCleanup()

	report := drift.Compare(baseGraph, headGraph)
	report.BaseRef = args.BaseRef
	report.CompareRef = headRef
	return report, nil
}

// checkoutAndScan checks out a git ref into a worktree and scans it. The
// returned cleanup must be deferred by the caller. role is used in error
// messages ("base" / "head") so failures point at the right argument.
func (h *HandlerRegistry) checkoutAndScan(ctx context.Context, repoPath, ref, role string) (*model.ArchGraph, func(), error) {
	worktree, cleanup, err := drift.CheckoutRef(ctx, repoPath, ref)
	if err != nil {
		return nil, nil, fmt.Errorf("checking out %s ref %s: %w", role, ref, err)
	}
	graph, err := h.scanner.Scan(worktree)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("scanning %s ref: %w", role, err)
	}
	return graph, cleanup, nil
}

// =============================================================================
// arch_drift_explain (drift + narrative)
// =============================================================================

type ArchDriftExplainArgs struct {
	Path    string `json:"path"`
	Repo    string `json:"repo,omitempty"`
	BaseRef string `json:"base_ref"`
	HeadRef string `json:"head_ref,omitempty"`
}

type ArchDriftExplainResult struct {
	Narrative string            `json:"narrative"`
	Report    *model.DiffReport `json:"report"`
}

func (h *HandlerRegistry) archDriftExplain(ctx context.Context, args ArchDriftExplainArgs) (*ArchDriftExplainResult, error) {
	report, err := h.archDrift(ctx, ArchDriftArgs(args))
	if err != nil {
		return nil, err
	}
	return &ArchDriftExplainResult{
		Narrative: drift.Narrate(report),
		Report:    report,
	}, nil
}
