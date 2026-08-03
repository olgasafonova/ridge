package tools

import (
	"context"
	"errors"
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
	Path string `json:"path" jsonschema:"Absolute filesystem path to the codebase to analyze. Supply either path or repo; path takes precedence when both are set."`
	Repo string `json:"repo,omitempty" jsonschema:"Alias of a codebase previously registered with arch_registry_add, used instead of path. Supply either path or repo; path takes precedence when both are set."`
	// Baseline selects the known-violations ratchet mode: "" (off),
	// "write" (save current violations as the accepted baseline), or
	// "check" (fail only on violations not in the baseline).
	Baseline     string `json:"baseline,omitempty" jsonschema:"Known-violations ratchet mode: omit to report every violation, write to record the current violations as the accepted baseline, or check to fail only on violations absent from that baseline."`
	BaselineFile string `json:"baseline_file,omitempty" jsonschema:"Path to the baseline file that the baseline mode reads or writes. Defaults to a conventional path in the codebase when omitted."`
	ScanControl
}

type ArchValidateResult struct {
	Valid         bool     `json:"valid"`
	Violations    []string `json:"violations"`
	NewViolations []string `json:"new_violations,omitempty"`
	Grandfathered int      `json:"grandfathered,omitempty"`
	BaselineFile  string   `json:"baseline_file,omitempty"`
	Summary       string   `json:"summary"`
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

	customRules := h.loadCustomRules(path)
	detectedViolations := detector.ValidateGraph(graph, customRules)

	if args.Baseline == "" {
		violations := formatViolations(detectedViolations)
		return &ArchValidateResult{
			Valid:      len(violations) == 0,
			Violations: violations,
			Summary:    fmt.Sprintf("Validation complete: %d violations found", len(violations)),
		}, nil
	}

	file, err := baselineFilePath(args.BaselineFile, path)
	if err != nil {
		return nil, err
	}
	switch args.Baseline {
	case "write":
		return writeValidateBaseline(file, detectedViolations)
	case "check":
		return checkValidateBaseline(file, detectedViolations)
	default:
		return nil, fmt.Errorf("invalid baseline mode %q: must be \"write\" or \"check\" (or omit for plain validation)", args.Baseline)
	}
}

// formatViolations renders violations in the [severity] rule: detail shape
// used across validation results.
func formatViolations(violations []detector.Violation) []string {
	out := make([]string, 0, len(violations))
	for _, v := range violations {
		out = append(out, fmt.Sprintf("[%s] %s: %s", v.Severity, v.Rule, v.Detail))
	}
	return out
}

// baselineFilePath resolves the baseline location: explicit override or the
// default .arch-known-violations.json at the repo root. The path is contained
// to the scanned repo in both modes — write for obvious reasons, check so a
// crafted baseline_file can't read files outside the repo.
func baselineFilePath(override, repoPath string) (string, error) {
	file := override
	if file == "" {
		file = filepath.Join(repoPath, detector.BaselineFileName)
	}
	if err := safepath.ValidateOutputPath(file, repoPath); err != nil {
		return "", fmt.Errorf("invalid baseline file path: %w", err)
	}
	return file, nil
}

// writeValidateBaseline grandfathers the current violation set: subsequent
// baseline="check" runs fail only on violations that aren't in this file.
// This is the adoption ratchet — strict rules on a legacy codebase without a
// red build on day one. Commit the file so the whole team shares the ratchet.
func writeValidateBaseline(file string, detected []detector.Violation) (*ArchValidateResult, error) {
	baseline := detector.NewBaseline(detected)
	if err := detector.SaveBaseline(file, baseline); err != nil {
		return nil, err
	}
	formatted := formatViolations(detected)
	return &ArchValidateResult{
		Valid:        len(formatted) == 0,
		Violations:   formatted,
		BaselineFile: file,
		Summary:      fmt.Sprintf("Baseline written: %d known violations grandfathered to %s; future baseline=\"check\" runs fail only on new violations", len(baseline.Violations), file),
	}, nil
}

// checkValidateBaseline validates against the saved baseline. Valid reflects
// only NEW violations; the full list stays in Violations so a baseline can
// change the verdict but never hide findings.
func checkValidateBaseline(file string, detected []detector.Violation) (*ArchValidateResult, error) {
	baseline, err := detector.LoadBaseline(file)
	if err != nil {
		if os.IsNotExist(errors.Unwrap(err)) {
			return nil, fmt.Errorf("no baseline found at %s: run arch_validate with baseline=\"write\" first to grandfather current violations", file)
		}
		return nil, err
	}
	fresh, grandfathered := detector.SplitKnown(detected, baseline)
	formatted := formatViolations(detected)
	return &ArchValidateResult{
		Valid:         len(fresh) == 0,
		Violations:    formatted,
		NewViolations: formatViolations(fresh),
		Grandfathered: grandfathered,
		BaselineFile:  file,
		Summary:       fmt.Sprintf("Validation complete: %d violations found (%d new, %d grandfathered by baseline)", len(formatted), len(fresh), grandfathered),
	}, nil
}

// =============================================================================
// arch_history
// =============================================================================

type ArchHistoryArgs struct {
	Path  string `json:"path" jsonschema:"Absolute filesystem path to the codebase to analyze. Supply either path or repo; path takes precedence when both are set."`
	Repo  string `json:"repo,omitempty" jsonschema:"Alias of a codebase previously registered with arch_registry_add, used instead of path. Supply either path or repo; path takes precedence when both are set."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of history entries to return, most recent first. Unset returns all recorded entries."`
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

	results, err := h.scanCommitsParallel(ctx, path, commits)
	if err != nil {
		return nil, err
	}

	annotateChangesFromPrev(results)

	entries := make([]drift.HistoryEntry, len(results))
	for i, r := range results {
		entries[i] = r.entry
	}

	return &ArchHistoryResult{
		Entries: entries,
		Summary: fmt.Sprintf("Analyzed %d commits", len(entries)),
	}, nil
}

// scanCommitsParallel checks out each commit in its own worktree and scans it,
// up to 4 concurrently. Each goroutine writes to its own slot, so no locking
// is needed. Per-commit scan failures are non-fatal: the entry survives with
// zero counts.
func (h *HandlerRegistry) scanCommitsParallel(ctx context.Context, path string, commits []drift.GitLogEntry) ([]scanResult, error) {
	results := make([]scanResult, len(commits))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4)

	for idx, c := range commits {
		results[idx].entry = drift.HistoryEntry{
			Ref:     c.Hash[:8],
			Date:    c.Date,
			Message: c.Message,
		}
		g.Go(func() error {
			h.scanCommitInto(gctx, path, c.Hash, &results[idx])
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("scanning commits: %w", err)
	}
	return results, nil
}

// scanCommitInto checks out a single commit and populates the result's graph
// and entry counts. Errors are silently swallowed so one bad commit doesn't
// abort the whole history scan.
func (h *HandlerRegistry) scanCommitInto(ctx context.Context, path, hash string, r *scanResult) {
	worktree, cleanup, wErr := drift.CheckoutRef(ctx, path, hash)
	if wErr != nil {
		return
	}
	defer cleanup()
	graph, scanErr := h.scanner.Scan(worktree)
	if scanErr != nil {
		return
	}
	r.graph = graph
	r.entry.NodeCount = graph.NodeCount()
	r.entry.EdgeCount = graph.EdgeCount()
	r.entry.Topology = string(graph.Topology)
}

// annotateChangesFromPrev walks results oldest-first and records the number of
// drift changes between each commit and the prior one that scanned successfully.
// commits arrive most-recent-first, so iteration goes end → start.
func annotateChangesFromPrev(results []scanResult) {
	var prevGraph *model.ArchGraph
	for i := len(results) - 1; i >= 0; i-- {
		r := &results[i]
		if r.graph == nil {
			continue
		}
		if prevGraph != nil {
			report := drift.Compare(prevGraph, r.graph)
			r.entry.ChangesFromPrevious = len(report.Changes)
		}
		prevGraph = r.graph
	}
}
