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

	walkedPaths, extByPath := indexFileWork(files)
	changes, detectErr := state.DetectChanges(walkedPaths)
	if detectErr != nil {
		s.logger.Warn("Change detection failed, falling back to full scan", "error", detectErr)
		return files, nil
	}

	toAnalyze = collectChangedWork(changes.Added, changes.Modified, extByPath)
	stats.FilesChanged = len(toAnalyze)

	unchangedAnalyze, cached := classifyUnchanged(state, changes.Unchanged, extByPath, stats)
	toAnalyze = append(toAnalyze, unchangedAnalyze...)

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

// indexFileWork returns parallel slices of paths and a path→ext lookup map.
func indexFileWork(files []fileWork) ([]string, map[string]string) {
	paths := make([]string, len(files))
	extByPath := make(map[string]string, len(files))
	for i, f := range files {
		paths[i] = f.path
		extByPath[f.path] = f.ext
	}
	return paths, extByPath
}

// collectChangedWork rehydrates fileWork entries for paths reported as added or
// modified by change detection.
func collectChangedWork(added, modified []string, extByPath map[string]string) []fileWork {
	out := make([]fileWork, 0, len(added)+len(modified))
	for _, path := range added {
		out = append(out, fileWork{path: path, ext: extByPath[path]})
	}
	for _, path := range modified {
		out = append(out, fileWork{path: path, ext: extByPath[path]})
	}
	return out
}

// classifyUnchanged splits unchanged paths into two buckets: those with cached
// analyzer output (reused as-is) and those without (must be re-analyzed).
// Updates stats.FilesCached for every reused result.
func classifyUnchanged(state *ScanState, unchanged []string, extByPath map[string]string, stats *ScanStats) (toAnalyze []fileWork, cached []analyzeResult) {
	for _, path := range unchanged {
		nodes, edges, ok := state.CachedResult(path)
		if ok {
			cached = append(cached, analyzeResult{nodes: nodes, edges: edges, path: path})
			stats.FilesCached++
			continue
		}
		toAnalyze = append(toAnalyze, fileWork{path: path, ext: extByPath[path]})
	}
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
