package tools

import (
	"context"
	"fmt"

	"github.com/olgasafonova/ridge/internal/detector"
	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/safepath"
)

// =============================================================================
// arch_dataflow
// =============================================================================

type ArchDataflowArgs struct {
	Path string `json:"path"`
	Repo string `json:"repo,omitempty"`
	ScanControl
}

type ArchDataflowResult struct {
	Endpoints []string             `json:"endpoints"`
	DataPaths []string             `json:"data_paths"`
	Traces    []model.ProcessTrace `json:"traces"`
	Summary   string               `json:"summary"`
}

func (h *HandlerRegistry) archDataflow(ctx context.Context, args ArchDataflowArgs) (*ArchDataflowResult, error) {
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

	endpoints := ensureSlice(collectEndpointNames(graph))
	dataPaths := ensureSlice(collectDataPaths(graph))
	traces := detector.ComputeTraces(graph)

	return &ArchDataflowResult{
		Endpoints: endpoints,
		DataPaths: dataPaths,
		Traces:    traces,
		Summary:   fmt.Sprintf("Found %d endpoints, %d data paths, %d process traces", len(endpoints), len(dataPaths), len(traces)),
	}, nil
}

// collectEndpointNames returns the names of all endpoint nodes in the graph.
func collectEndpointNames(graph *model.ArchGraph) []string {
	nodes := graph.NodesByType(model.NodeEndpoint)
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Name)
	}
	return out
}

// collectDataPaths returns formatted "source -> target (label)" strings for
// every dataflow or read/write edge in the graph.
func collectDataPaths(graph *model.ArchGraph) []string {
	var out []string
	for _, e := range graph.Edges() {
		if e.Type == model.EdgeDataFlow || e.Type == model.EdgeReadWrite {
			out = append(out, fmt.Sprintf("%s -> %s (%s)", e.Source, e.Target, e.Label))
		}
	}
	return out
}

// =============================================================================
// arch_boundaries
// =============================================================================

type ArchBoundariesArgs struct {
	Path string `json:"path"`
	Repo string `json:"repo,omitempty"`
}

type BoundaryInfo struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Type    string   `json:"type"`
	Markers []string `json:"markers"`
}

type ArchBoundariesResult struct {
	Topology   string                   `json:"topology"`
	Boundaries []BoundaryInfo           `json:"boundaries"`
	Signals    detector.TopologySignals `json:"signals"`   // evidence behind the verdict
	Reason     string                   `json:"reason"`    // which rule produced the topology
	Ambiguous  bool                     `json:"ambiguous"` // true when the verdict is borderline
	Summary    string                   `json:"summary"`
}

func (h *HandlerRegistry) archBoundaries(_ context.Context, args ArchBoundariesArgs) (*ArchBoundariesResult, error) {
	path, _, err := h.resolveRepoPath(args.Path, args.Repo)
	if err != nil {
		return nil, err
	}
	if err := safepath.ValidateScanPath(path); err != nil {
		return nil, err
	}

	result, err := detector.DetectBoundaries(path)
	if err != nil {
		return nil, fmt.Errorf("detecting boundaries: %w", err)
	}

	var boundaries []BoundaryInfo
	for _, b := range result.Boundaries {
		boundaries = append(boundaries, BoundaryInfo{
			Name:    b.Name,
			Path:    b.Path,
			Type:    b.Type,
			Markers: b.Markers,
		})
	}

	if boundaries == nil {
		boundaries = []BoundaryInfo{}
	}

	summary := fmt.Sprintf("Detected %s topology with %d boundaries (%s)",
		result.Topology, len(boundaries), result.Reason)
	if result.Ambiguous {
		summary += " — verdict is borderline; check signals"
	}

	return &ArchBoundariesResult{
		Topology:   string(result.Topology),
		Boundaries: boundaries,
		Signals:    result.Signals,
		Reason:     result.Reason,
		Ambiguous:  result.Ambiguous,
		Summary:    summary,
	}, nil
}
