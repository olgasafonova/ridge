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

	var endpoints, dataPaths []string
	for _, n := range graph.NodesByType(model.NodeEndpoint) {
		endpoints = append(endpoints, n.Name)
	}
	for _, e := range graph.Edges() {
		if e.Type == model.EdgeDataFlow || e.Type == model.EdgeReadWrite {
			dataPaths = append(dataPaths, fmt.Sprintf("%s -> %s (%s)", e.Source, e.Target, e.Label))
		}
	}

	if endpoints == nil {
		endpoints = []string{}
	}
	if dataPaths == nil {
		dataPaths = []string{}
	}

	traces := detector.ComputeTraces(graph)

	return &ArchDataflowResult{
		Endpoints: endpoints,
		DataPaths: dataPaths,
		Traces:    traces,
		Summary:   fmt.Sprintf("Found %d endpoints, %d data paths, %d process traces", len(endpoints), len(dataPaths), len(traces)),
	}, nil
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
	Topology   string         `json:"topology"`
	Boundaries []BoundaryInfo `json:"boundaries"`
	Summary    string         `json:"summary"`
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

	return &ArchBoundariesResult{
		Topology:   string(result.Topology),
		Boundaries: boundaries,
		Summary:    fmt.Sprintf("Detected %s topology with %d boundaries", result.Topology, len(boundaries)),
	}, nil
}
