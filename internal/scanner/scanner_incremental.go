package scanner

import (
	"github.com/olgasafonova/ridge/internal/model"
)

// analyzeResult holds the output of analyzing a single file.
type analyzeResult struct {
	path  string // source file path (for state updates)
	ext   string // file extension (used to look up the analyzer signature)
	nodes []*model.Node
	edges []*model.Edge
}

// partitionForIncremental performs Phase 1.5: split walked files into work
// that needs analysis and cached results that can be reused. Without state,
// every file goes into toAnalyze. Updates stats.FilesChanged / FilesCached /
// FilesInvalidated.
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

	sigByExt := s.signatureByExt()
	unchangedAnalyze, cached := classifyUnchanged(state, changes.Unchanged, extByPath, sigByExt, stats)
	toAnalyze = append(toAnalyze, unchangedAnalyze...)

	for _, path := range changes.Deleted {
		state.RemoveFile(path)
	}

	s.logger.Info("Incremental change detection",
		"unchanged", len(changes.Unchanged),
		"added", len(changes.Added),
		"modified", len(changes.Modified),
		"deleted", len(changes.Deleted),
		"invalidated", stats.FilesInvalidated,
	)
	return toAnalyze, cached
}

// signatureByExt builds a map of file extension → current analyzer signature.
// Used to decide whether a cached FileEntry was produced by an analyzer whose
// behavior still matches the current binary.
func (s *Scanner) signatureByExt() map[string]string {
	out := make(map[string]string, len(s.analyzers))
	for ext, a := range s.analyzers {
		out[ext] = a.Signature()
	}
	return out
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
// analyzer output that matches the current analyzer signature (reused as-is)
// and those whose cache is missing or stale (must be re-analyzed). Updates
// stats.FilesCached for cache hits and stats.FilesInvalidated for entries
// rejected due to signature mismatch.
func classifyUnchanged(state *ScanState, unchanged []string, extByPath map[string]string, sigByExt map[string]string, stats *ScanStats) (toAnalyze []fileWork, cached []analyzeResult) {
	for _, path := range unchanged {
		ext := extByPath[path]
		cachedSig, nodes, edges, ok := state.CachedResult(path)
		if !ok {
			toAnalyze = append(toAnalyze, fileWork{path: path, ext: ext})
			continue
		}
		if cachedSig != sigByExt[ext] {
			// Analyzer behavior has changed since this entry was cached
			// (or entry predates signature tracking). Re-analyze.
			toAnalyze = append(toAnalyze, fileWork{path: path, ext: ext})
			stats.FilesInvalidated++
			continue
		}
		cached = append(cached, analyzeResult{nodes: nodes, edges: edges, path: path, ext: ext})
		stats.FilesCached++
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
