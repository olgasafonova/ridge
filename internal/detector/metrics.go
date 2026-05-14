package detector

import (
	"github.com/olgasafonova/ridge/internal/model"
)

// Metrics holds architecture fitness scores.
type Metrics struct {
	Components     map[string]int     `json:"components"`      // node type → count
	EdgeCounts     map[string]int     `json:"edge_counts"`     // edge type → count
	Coupling       map[string]float64 `json:"coupling"`        // nodeID → fan-out count
	Instability    map[string]float64 `json:"instability"`     // nodeID → I metric (0=stable, 1=unstable)
	MaxDepth       int                `json:"max_depth"`       // longest dependency chain
	AvgCoupling    float64            `json:"avg_coupling"`    // mean fan-out
	AvgInstability float64            `json:"avg_instability"` // mean instability
}

// ComputeMetrics calculates architecture fitness metrics from a graph.
// Import edges ("import:X" targets) are resolved to real package node IDs
// before computing fan-in/fan-out and dependency depth.
func ComputeMetrics(graph *model.ArchGraph) *Metrics {
	m := &Metrics{
		Components:  componentCounts(graph),
		EdgeCounts:  make(map[string]int),
		Coupling:    make(map[string]float64),
		Instability: make(map[string]float64),
	}

	resolved := graph.ResolvedEdges()
	for _, e := range resolved {
		m.EdgeCounts[string(e.Type)]++
	}

	fanOutMap, fanInMap := computeFanInOut(resolved)
	populateCouplingMetrics(m, graph.Nodes(), fanOutMap, fanInMap)
	m.MaxDepth = computeMaxDepth(resolved, graph.Nodes())

	return m
}

// componentCounts counts nodes by type.
func componentCounts(graph *model.ArchGraph) map[string]int {
	counts := make(map[string]int)
	for _, n := range graph.Nodes() {
		counts[string(n.Type)]++
	}
	return counts
}

// computeFanInOut builds fan-in/fan-out maps from dependency edges.
func computeFanInOut(edges []*model.Edge) (fanOut, fanIn map[string]int) {
	fanOut = make(map[string]int)
	fanIn = make(map[string]int)
	for _, e := range edges {
		if e.Type != model.EdgeDependency {
			continue
		}
		fanOut[e.Source]++
		fanIn[e.Target]++
	}
	return fanOut, fanIn
}

// populateCouplingMetrics fills per-node coupling/instability and averages.
func populateCouplingMetrics(m *Metrics, nodes []*model.Node, fanOut, fanIn map[string]int) {
	var couplingSum, instabilitySum float64
	var count int
	for _, n := range nodes {
		if isInfraNode(n.Type) {
			continue
		}
		out := fanOut[n.ID]
		in := fanIn[n.ID]
		m.Coupling[n.ID] = float64(out)
		if total := in + out; total > 0 {
			m.Instability[n.ID] = float64(out) / float64(total)
		}
		couplingSum += float64(out)
		instabilitySum += m.Instability[n.ID]
		count++
	}
	if count > 0 {
		m.AvgCoupling = couplingSum / float64(count)
		m.AvgInstability = instabilitySum / float64(count)
	}
}

func isInfraNode(t model.NodeType) bool {
	return t == model.NodeDatabase || t == model.NodeQueue ||
		t == model.NodeCache || t == model.NodeExternalAPI
}

// dependencyAdjacency builds an adjacency list for dependency edges.
func dependencyAdjacency(edges []*model.Edge) map[string][]string {
	adj := make(map[string][]string)
	for _, e := range edges {
		if e.Type == model.EdgeDependency {
			adj[e.Source] = append(adj[e.Source], e.Target)
		}
	}
	return adj
}

// longestChainFn returns a memoized function that yields the longest
// dependency chain rooted at id. Back-edges contribute zero to break cycles.
func longestChainFn(adj map[string][]string) func(id string) int {
	memo := make(map[string]int)
	visiting := make(map[string]bool)
	var longest func(id string) int
	longest = func(id string) int {
		if v, ok := memo[id]; ok {
			return v
		}
		if visiting[id] {
			return 0
		}
		visiting[id] = true
		best := 0
		for _, next := range adj[id] {
			if d := 1 + longest(next); d > best {
				best = d
			}
		}
		visiting[id] = false
		memo[id] = best
		return best
	}
	return longest
}

// computeMaxDepth finds the longest dependency chain in the graph.
// Uses DFS with memoization (valid because the graph is a DAG).
func computeMaxDepth(edges []*model.Edge, nodes []*model.Node) int {
	longest := longestChainFn(dependencyAdjacency(edges))
	maxDepth := 0
	for _, n := range nodes {
		if isInfraNode(n.Type) {
			continue
		}
		if d := longest(n.ID); d > maxDepth {
			maxDepth = d
		}
	}
	return maxDepth
}
