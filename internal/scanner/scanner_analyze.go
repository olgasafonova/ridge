package scanner

import (
	"context"
	"runtime"
	"sync"

	"github.com/olgasafonova/ridge/internal/model"
)

// cloneAnalyzers creates independent copies of all registered analyzers.
func (s *Scanner) cloneAnalyzers() map[string]Analyzer {
	cloned := make(map[string]Analyzer, len(s.analyzers))
	for ext, a := range s.analyzers {
		cloned[ext] = a.Clone()
	}
	return cloned
}

// chooseWorkers picks the worker count: opts override or NumCPU, capped at 8
// and clamped to the size of the work queue.
func chooseWorkers(requested, queueSize int) int {
	w := requested
	if w <= 0 {
		w = runtime.NumCPU()
	}
	if w > 8 {
		w = 8
	}
	if w > queueSize {
		w = queueSize
	}
	return w
}

// runAnalysis dispatches to either the sequential or parallel analyzer path.
// Returns true if a limit was hit during analysis.
func (s *Scanner) runAnalysis(ctx context.Context, toAnalyze []fileWork, workers int, state *ScanState, graph *model.ArchGraph, stats *ScanStats, maxNodes int) bool {
	switch {
	case len(toAnalyze) == 0:
		return false
	case workers <= 1:
		return s.analyzeSequential(ctx, toAnalyze, state, graph, stats, maxNodes)
	default:
		return s.analyzeParallel(ctx, toAnalyze, workers, state, graph, stats, maxNodes)
	}
}

// analyzeSequential runs analyzers on the calling goroutine. Used when workers <= 1.
func (s *Scanner) analyzeSequential(ctx context.Context, toAnalyze []fileWork, state *ScanState, graph *model.ArchGraph, stats *ScanStats, maxNodes int) bool {
	for _, f := range toAnalyze {
		if ctx.Err() != nil {
			return true
		}
		nodes, edges, err := s.analyzers[f.ext].Analyze(f.path)
		if err != nil {
			s.logger.Warn("Analyzer error", "path", f.path, "error", err)
			continue
		}
		stats.FilesAnalyzed++
		s.maybeUpdateState(state, f.path, nodes, edges)
		if mergeNodesAndEdges(graph, nodes, edges, stats, maxNodes) {
			return true
		}
	}
	return false
}

// analyzeParallel fans out analyzer work across N goroutines, with a single
// goroutine collecting results to keep graph mutation single-threaded.
func (s *Scanner) analyzeParallel(ctx context.Context, toAnalyze []fileWork, workers int, state *ScanState, graph *model.ArchGraph, stats *ScanStats, maxNodes int) bool {
	workCh := make(chan fileWork, len(toAnalyze))
	resultCh := make(chan analyzeResult, len(toAnalyze))

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go s.worker(ctx, &wg, workCh, resultCh)
	}
	for _, f := range toAnalyze {
		workCh <- f
	}
	close(workCh)
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	truncated := false
	for r := range resultCh {
		stats.FilesAnalyzed++
		s.maybeUpdateState(state, r.path, r.nodes, r.edges)
		if !truncated && mergeNodesAndEdges(graph, r.nodes, r.edges, stats, maxNodes) {
			truncated = true
			// keep draining resultCh so workers can finish; max-nodes bail-out
			// must not deadlock
		}
	}
	return truncated
}

// worker is one fan-out goroutine: clones analyzers (for thread-safety) and
// drains workCh, posting analyzeResults to resultCh.
func (s *Scanner) worker(ctx context.Context, wg *sync.WaitGroup, workCh <-chan fileWork, resultCh chan<- analyzeResult) {
	defer wg.Done()
	cloned := s.cloneAnalyzers()
	for f := range workCh {
		if ctx.Err() != nil {
			continue // drain channel
		}
		nodes, edges, err := cloned[f.ext].Analyze(f.path)
		if err != nil {
			s.logger.Warn("Analyzer error", "path", f.path, "error", err)
			continue
		}
		resultCh <- analyzeResult{nodes: nodes, edges: edges, path: f.path}
	}
}

// mergeNodesAndEdges adds an analyzer's output into the graph, honoring maxNodes.
// Returns true if maxNodes stopped the merge mid-way.
func mergeNodesAndEdges(graph *model.ArchGraph, nodes []*model.Node, edges []*model.Edge, stats *ScanStats, maxNodes int) bool {
	for _, n := range nodes {
		if maxNodes > 0 && stats.NodesFound >= maxNodes {
			return true
		}
		if graph.AddNode(n) {
			stats.NodesFound++
		}
	}
	for _, e := range edges {
		graph.AddEdge(e)
		stats.EdgesFound++
	}
	return false
}

// maybeUpdateState writes fresh analyzer output into the persistent ScanState
// if state is non-nil; logs and continues on error.
func (s *Scanner) maybeUpdateState(state *ScanState, path string, nodes []*model.Node, edges []*model.Edge) {
	if state == nil {
		return
	}
	if err := state.UpdateFile(path, nodes, edges); err != nil {
		s.logger.Warn("State update error", "path", path, "error", err)
	}
}
