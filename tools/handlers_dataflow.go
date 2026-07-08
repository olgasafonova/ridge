package tools

import (
	"context"
	"fmt"
	"strings"

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
	ScanControl
}

type BoundaryInfo struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Type    string   `json:"type"`
	Markers []string `json:"markers"`
}

type ArchBoundariesResult struct {
	Topology     string                   `json:"topology"`
	Boundaries   []BoundaryInfo           `json:"boundaries"`
	Signals      detector.TopologySignals `json:"signals"`                 // filesystem-marker evidence behind the verdict
	GraphSignals *detector.GraphSignals   `json:"graph_signals,omitempty"` // scan-derived evidence (cross-boundary edges, shared DB); nil when the scan fails
	Reason       string                   `json:"reason"`                  // which rule produced the topology
	Ambiguous    bool                     `json:"ambiguous"`               // true when the verdict is borderline
	Summary      string                   `json:"summary"`
}

func (h *HandlerRegistry) archBoundaries(ctx context.Context, args ArchBoundariesArgs) (*ArchBoundariesResult, error) {
	path, alias, err := h.resolveRepoPath(args.Path, args.Repo)
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

	// Best-effort scan for graph-derived evidence. Boundary detection itself
	// is marker-based and must keep working when a scan cannot (unsupported
	// language mix, scan limits), so a scan failure degrades to nil signals
	// instead of failing the tool.
	var graphSignals *detector.GraphSignals
	if graph, _, scanErr := h.scanPath(ctx, path, alias, args.ScanControl); scanErr == nil {
		graphSignals = detector.ComputeGraphSignals(result.Boundaries, graph)
	} else {
		h.logger.Warn("arch_boundaries: scan for graph signals failed; returning marker evidence only",
			"path", path, "error", scanErr)
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
	summary += fmt.Sprintf("; %d deployable units", result.Signals.DeployableUnits)
	if graphSignals != nil {
		shared := "no shared database"
		if graphSignals.SharedDatabase {
			shared = fmt.Sprintf("shared database (%s)", strings.Join(graphSignals.SharedDatabaseIDs, ", "))
		}
		summary += fmt.Sprintf(", %d cross-boundary edges, %s", graphSignals.CrossBoundaryEdges, shared)
	}
	if result.Ambiguous {
		summary += " — verdict is borderline; check signals"
	}

	return &ArchBoundariesResult{
		Topology:     string(result.Topology),
		Boundaries:   boundaries,
		Signals:      result.Signals,
		GraphSignals: graphSignals,
		Reason:       result.Reason,
		Ambiguous:    result.Ambiguous,
		Summary:      summary,
	}, nil
}
