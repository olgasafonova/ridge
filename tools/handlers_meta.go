package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/olgasafonova/ridge/internal/detector"
	"github.com/olgasafonova/ridge/internal/drift"
	"github.com/olgasafonova/ridge/internal/safepath"
)

// =============================================================================
// arch_snapshot
// =============================================================================

type ArchSnapshotArgs struct {
	Path       string `json:"path"`
	Repo       string `json:"repo,omitempty"`
	OutputFile string `json:"output_file,omitempty"`
	Label      string `json:"label,omitempty"`
	ScanControl
}

type ArchSnapshotResult struct {
	File      string `json:"file"`
	NodeCount int    `json:"node_count"`
	EdgeCount int    `json:"edge_count"`
	Summary   string `json:"summary"`
}

func (h *HandlerRegistry) archSnapshot(ctx context.Context, args ArchSnapshotArgs) (*ArchSnapshotResult, error) {
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

	outFile := args.OutputFile
	if outFile == "" {
		outFile = filepath.Join(path, "architecture.snapshot.json")
	}
	if err := safepath.ValidateOutputPath(outFile, path); err != nil {
		return nil, fmt.Errorf("invalid output file path: %w", err)
	}

	snap, err := drift.Save(graph, outFile, args.Label)
	if err != nil {
		return nil, fmt.Errorf("saving snapshot: %w", err)
	}

	return &ArchSnapshotResult{
		File:      outFile,
		NodeCount: len(snap.Nodes),
		EdgeCount: len(snap.Edges),
		Summary:   fmt.Sprintf("Saved snapshot with %d nodes and %d edges to %s", len(snap.Nodes), len(snap.Edges), outFile),
	}, nil
}

// =============================================================================
// arch_metrics
// =============================================================================

type ArchMetricsArgs struct {
	Path string `json:"path"`
	Repo string `json:"repo,omitempty"`
	ScanControl
}

type ArchMetricsResult struct {
	*detector.Metrics
	Summary string `json:"summary"`
}

func (h *HandlerRegistry) archMetrics(ctx context.Context, args ArchMetricsArgs) (*ArchMetricsResult, error) {
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

	metrics := detector.ComputeMetrics(graph)

	return &ArchMetricsResult{
		Metrics: metrics,
		Summary: fmt.Sprintf("Analyzed %d components: avg coupling %.1f, avg instability %.2f, max depth %d",
			graph.NodeCount(), metrics.AvgCoupling, metrics.AvgInstability, metrics.MaxDepth),
	}, nil
}

// =============================================================================
// arch_explain
// =============================================================================

type ArchExplainArgs struct {
	Path     string `json:"path"`
	Repo     string `json:"repo,omitempty"`
	Question string `json:"question,omitempty"`
	ScanControl
}

type ArchExplainResult struct {
	Explanation string   `json:"explanation"`
	Evidence    []string `json:"evidence"`
}

func (h *HandlerRegistry) archExplain(ctx context.Context, args ArchExplainArgs) (*ArchExplainResult, error) {
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

	boundaries, _ := detector.DetectBoundaries(path)
	explanation := detector.ExplainArchitecture(graph, boundaries)

	// Build evidence from patterns, decisions, and risks
	var evidence []string
	evidence = append(evidence, "Topology: "+explanation.TopologyReason)
	for _, p := range explanation.Patterns {
		evidence = append(evidence, "Pattern: "+p)
	}
	for _, d := range explanation.KeyDecisions {
		evidence = append(evidence, "Decision: "+d)
	}
	for _, r := range explanation.Risks {
		evidence = append(evidence, "Risk: "+r)
	}

	return &ArchExplainResult{
		Explanation: explanation.Summary,
		Evidence:    evidence,
	}, nil
}

// =============================================================================
// arch_recommend
// =============================================================================

type ArchRecommendArgs struct {
	Path  string `json:"path"`
	Repo  string `json:"repo,omitempty"`
	Focus string `json:"focus,omitempty"` // filter by recommendation category
	ScanControl
}

type ArchRecommendResult struct {
	Recommendations []detector.Recommendation `json:"recommendations"`
	Summary         string                    `json:"summary"`
	MetricsSnapshot *detector.Metrics         `json:"metrics_snapshot"`
}

func (h *HandlerRegistry) archRecommend(ctx context.Context, args ArchRecommendArgs) (*ArchRecommendResult, error) {
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
	violations := detector.ValidateGraph(graph, customRules)
	metrics := detector.ComputeMetrics(graph)
	boundaries, _ := detector.DetectBoundaries(args.Path)
	explanation := detector.ExplainArchitecture(graph, boundaries)

	recs := detector.RecommendArchitecture(graph, violations, metrics, explanation)
	recs = filterRecommendations(recs, args.Focus)

	if recs == nil {
		recs = []detector.Recommendation{}
	}

	return &ArchRecommendResult{
		Recommendations: recs,
		Summary:         fmt.Sprintf("Generated %d recommendations for %s", len(recs), path),
		MetricsSnapshot: metrics,
	}, nil
}

// inRepoRulesEnv opts users into loading .arch-rules.yaml from inside an
// analyzed repo. Without it, the file is ignored: an attacker who controls
// the scanned content (third-party repo, prompt-injection-reachable path)
// could otherwise ship a rules file that downgrades their own violations.
const inRepoRulesEnv = "RIDGE_ALLOW_INREPO_RULES"

// loadCustomRules returns the parsed .arch-rules.yaml from the repo root, or
// nil if no rules file exists, the opt-in env var is unset, or parsing fails.
func (h *HandlerRegistry) loadCustomRules(repoPath string) *detector.RulesConfig {
	rulesPath := filepath.Join(repoPath, ".arch-rules.yaml")
	if _, err := os.Stat(rulesPath); err != nil {
		return nil
	}
	if os.Getenv(inRepoRulesEnv) != "1" {
		h.logger.Warn(
			"ignored .arch-rules.yaml inside analyzed repo (confused-deputy risk); set env=1 to opt in for trusted repos",
			"path", rulesPath,
			"env", inRepoRulesEnv,
		)
		return nil
	}
	rules, err := detector.LoadRules(rulesPath)
	if err != nil {
		h.logger.Warn("failed to parse custom rules", "path", rulesPath, "error", err)
		return nil
	}
	return rules
}

// filterRecommendations returns recs unchanged when category is empty, or only
// recommendations matching the requested category.
func filterRecommendations(recs []detector.Recommendation, category string) []detector.Recommendation {
	if category == "" {
		return recs
	}
	out := make([]detector.Recommendation, 0, len(recs))
	for _, r := range recs {
		if r.Category == category {
			out = append(out, r)
		}
	}
	return out
}
