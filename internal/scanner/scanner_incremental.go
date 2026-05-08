package scanner

import (
	"github.com/olgasafonova/ridge/internal/model"
)

// analyzeResult holds the output of analyzing a single file.
type analyzeResult struct {
	path  string // source file path (for state updates)
	nodes []*model.Node
	edges []*model.Edge
}

// partitionForIncremental performs Phase 1.5: split walked files into work
// that needs analysis and cached results that can be reused. Without state,
// every file goes into toAnalyze. Updates stats.FilesChanged / FilesCached.
func (s *Scanner) partitionForIncremental(files []fileWork, state *ScanState, stats *ScanStats) (toAnalyze []fileWork, cached []analyzeResult) {
	if state == nil {
		return files, nil
	}

	walkedPaths := make([]string, len(files))
	for i, f := range files {
		walkedPaths[i] = f.path
	}

	changes, detectErr := state.DetectChanges(walkedPaths)
	if detectErr != nil {
		s.logger.Warn("Change detection failed, falling back to full scan", "error", detectErr)
		return files, nil
	}

	extByPath := make(map[string]string, len(files))
	for _, f := range files {
		extByPath[f.path] = f.ext
	}

	for _, path := range changes.Added {
		toAnalyze = append(toAnalyze, fileWork{path: path, ext: extByPath[path]})
	}
	for _, path := range changes.Modified {
		toAnalyze = append(toAnalyze, fileWork{path: path, ext: extByPath[path]})
	}
	stats.FilesChanged = len(toAnalyze)

	for _, path := range changes.Unchanged {
		nodes, edges, ok := state.CachedResult(path)
		if ok {
			cached = append(cached, analyzeResult{nodes: nodes, edges: edges, path: path})
			stats.FilesCached++
		} else {
			toAnalyze = append(toAnalyze, fileWork{path: path, ext: extByPath[path]})
		}
	}

	for _, path := range changes.Deleted {
		state.RemoveFile(path)
	}

	s.logger.Info("Incremental change detection",
		"unchanged", len(changes.Unchanged),
		"added", len(changes.Added),
		"modified", len(changes.Modified),
		"deleted", len(changes.Deleted),
	)
	return toAnalyze, cached
}

// mergeCachedResults adds previously-analyzed nodes and edges into the new graph.
func mergeCachedResults(graph *model.ArchGraph, cached []analyzeResult, stats *ScanStats) {
	for _, r := range cached {
		for _, n := range r.nodes {
			if graph.AddNode(n) {
				stats.NodesFound++
			}
		}
		for _, e := range r.edges {
			graph.AddEdge(e)
			stats.EdgesFound++
		}
	}
}
