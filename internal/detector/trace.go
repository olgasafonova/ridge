package detector

import (
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

const maxTraceDepth = 20

// terminalNodeTypes are node types where a trace naturally terminates.
var terminalNodeTypes = map[model.NodeType]bool{
	model.NodeDatabase:    true,
	model.NodeQueue:       true,
	model.NodeCache:       true,
	model.NodeExternalAPI: true,
}

// traceWalker carries the per-walk state for one ComputeTraces invocation so
// the recursive DFS doesn't need to thread every input through its signature.
type traceWalker struct {
	graph  *model.ArchGraph
	adj    map[string][]*model.Edge
	seen   map[string]bool
	traces []model.ProcessTrace
}

// dfsFrame is the per-recursion-step state for the trace DFS. Bundling these
// keeps dfs/visitEdge under the function-argument threshold.
type dfsFrame struct {
	chain     []string
	edgeTypes []string
	minConf   float64
	visited   map[string]bool
}

// extend returns a frame with one more edge appended.
func (f dfsFrame) extend(targetID, edgeType string, conf float64) dfsFrame {
	return dfsFrame{
		chain:     append(f.chain, targetID),
		edgeTypes: append(f.edgeTypes, edgeType),
		minConf:   conf,
		visited:   f.visited,
	}
}

// ComputeTraces builds process traces from endpoint nodes through the resolved
// edge graph to terminal nodes (databases, queues, caches, external APIs) or
// leaf nodes with no outgoing edges. Traces with identical chains are deduplicated.
func ComputeTraces(graph *model.ArchGraph) []model.ProcessTrace {
	w := &traceWalker{
		graph: graph,
		adj:   buildAdjacency(graph.ResolvedEdges()),
		seen:  make(map[string]bool),
	}
	for _, ep := range graph.NodesByType(model.NodeEndpoint) {
		w.dfs(ep.ID, dfsFrame{
			chain:   []string{ep.ID},
			minConf: 1.0,
			visited: map[string]bool{ep.ID: true},
		})
	}
	if w.traces == nil {
		return []model.ProcessTrace{}
	}
	return w.traces
}

// buildAdjacency groups edges by source node ID.
func buildAdjacency(edges []*model.Edge) map[string][]*model.Edge {
	adj := make(map[string][]*model.Edge)
	for _, e := range edges {
		adj[e.Source] = append(adj[e.Source], e)
	}
	return adj
}

// dfs walks outgoing edges from nodeID, recording a trace whenever the chain
// reaches a leaf (no outgoing edges) or a terminal node type.
func (w *traceWalker) dfs(nodeID string, f dfsFrame) {
	if len(f.chain) > maxTraceDepth {
		return
	}
	outgoing := w.adj[nodeID]
	if len(outgoing) == 0 {
		w.recordLeaf(f)
		return
	}
	for _, e := range outgoing {
		w.visitEdge(e, f)
	}
}

// visitEdge extends the chain by one edge and either records a terminal trace
// or recurses, depending on the target node's type.
func (w *traceWalker) visitEdge(e *model.Edge, f dfsFrame) {
	if f.visited[e.Target] {
		return
	}
	conf := f.minConf
	if e.Confidence > 0 && e.Confidence < conf {
		conf = e.Confidence
	}
	next := f.extend(e.Target, string(e.Type), conf)

	if w.isTerminalTarget(e.Target) {
		w.recordTerminal(next, e.Target)
		return
	}
	f.visited[e.Target] = true
	w.dfs(e.Target, next)
	delete(f.visited, e.Target)
}

// isTerminalTarget reports whether the target node's type ends a trace.
func (w *traceWalker) isTerminalTarget(targetID string) bool {
	n := w.graph.GetNode(targetID)
	return n != nil && terminalNodeTypes[n.Type]
}

// recordLeaf appends a trace ending at a leaf node (no outgoing edges).
// Only records if the chain has at least two nodes.
func (w *traceWalker) recordLeaf(f dfsFrame) {
	if len(f.chain) < 2 {
		return
	}
	w.appendUnique(f, f.chain[len(f.chain)-1])
}

// recordTerminal appends a trace ending at a terminal node (database, queue, …).
func (w *traceWalker) recordTerminal(f dfsFrame, terminal string) {
	w.appendUnique(f, terminal)
}

// appendUnique adds a trace if its chain key hasn't been recorded yet.
func (w *traceWalker) appendUnique(f dfsFrame, terminal string) {
	key := strings.Join(f.chain, "|")
	if w.seen[key] {
		return
	}
	w.seen[key] = true
	w.traces = append(w.traces, model.ProcessTrace{
		EntryPoint: f.chain[0],
		Chain:      copySlice(f.chain),
		EdgeTypes:  copySlice(f.edgeTypes),
		Terminal:   terminal,
		Confidence: f.minConf,
	})
}

func copySlice(s []string) []string {
	c := make([]string, len(s))
	copy(c, s)
	return c
}
