package detector

import (
	"fmt"
	"sort"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// Violation represents a single architecture violation.
type Violation struct {
	Rule     string             `json:"rule"`
	Severity model.DiffSeverity `json:"severity"`
	Subject  string             `json:"subject"`
	Detail   string             `json:"detail"`
}

// ValidateGraph checks the graph against built-in and custom architecture rules.
func ValidateGraph(graph *model.ArchGraph, customRules *RulesConfig) []Violation {
	var violations []Violation
	violations = append(violations, checkCycles(graph)...)
	violations = append(violations, checkOrphans(graph)...)
	violations = append(violations, checkLayeringViolations(graph)...)
	violations = append(violations, CheckEnvironment(graph.RootPath)...)
	if customRules != nil {
		violations = append(violations, CheckCustomRules(graph, customRules, graph.RootPath)...)
	}
	return violations
}

// checkCycles detects circular dependencies.
func checkCycles(graph *model.ArchGraph) []Violation {
	// Find nodes participating in cycles using Tarjan's SCC over RESOLVED
	// edges: raw Go/TS import edges target unresolved "import:<path>" strings,
	// so a cycle mediated by imports (the common case) never closes in the
	// raw edge set. blast_radius and the renderers already resolve; validate
	// must too, or import cycles are structurally undetectable.
	sccs := tarjanSCC(graph)
	var violations []Violation
	for _, scc := range sccs {
		if len(scc) > 1 {
			// Sort membership so the Subject is deterministic across runs
			// and structurally identifies THIS cycle: baseline matching
			// (see baseline.go) keys on Rule+Subject, so two different
			// cycles of the same size must not collide.
			sorted := append([]string(nil), scc...)
			sort.Strings(sorted)
			violations = append(violations, Violation{
				Rule:     "no_circular_dependencies",
				Severity: model.SeverityCritical,
				Subject:  fmt.Sprintf("cycle(%d nodes): %s", len(sorted), strings.Join(sorted, ", ")),
				Detail:   fmt.Sprintf("Circular dependency among: %v", sorted),
			})
		}
	}

	// Fallback for cycles Tarjan-over-resolved can't see: ResolvedEdges
	// drops self-loops, so a raw-edge self-cycle still needs the generic
	// raw-edge check.
	if len(violations) == 0 && graph.HasCycle() {
		violations = append(violations, Violation{
			Rule:     "no_circular_dependencies",
			Severity: model.SeverityCritical,
			Subject:  "cycle",
			Detail:   "Circular dependency detected",
		})
	}

	return violations
}

// tarjanState bundles the running state of Tarjan's SCC algorithm. Pulling it
// out of tarjanSCC lets the recursive step live as a method on an explicit
// receiver rather than a closure, which flattens the function's complexity.
type tarjanState struct {
	adj       map[string][]string
	index     int
	nodeIndex map[string]int
	lowlink   map[string]int
	onStack   map[string]bool
	stack     []string
	sccs      [][]string
}

// tarjanSCC finds strongly connected components.
func tarjanSCC(graph *model.ArchGraph) [][]string {
	st := newTarjanState(graph)
	for _, n := range graph.Nodes() {
		if _, visited := st.nodeIndex[n.ID]; !visited {
			st.strongConnect(n.ID)
		}
	}
	return st.sccs
}

func newTarjanState(graph *model.ArchGraph) *tarjanState {
	adj := make(map[string][]string)
	// ResolvedEdges rewrites import:/wikilink: targets to concrete node IDs
	// and drops unresolvable ones, so SCCs form across real dependencies.
	for _, e := range graph.ResolvedEdges() {
		if e.Type == model.EdgeDependency {
			adj[e.Source] = append(adj[e.Source], e.Target)
		}
	}
	return &tarjanState{
		adj:       adj,
		nodeIndex: make(map[string]int),
		lowlink:   make(map[string]int),
		onStack:   make(map[string]bool),
	}
}

func (s *tarjanState) strongConnect(v string) {
	s.nodeIndex[v] = s.index
	s.lowlink[v] = s.index
	s.index++
	s.stack = append(s.stack, v)
	s.onStack[v] = true

	for _, w := range s.adj[v] {
		s.visitNeighbor(v, w)
	}

	if s.lowlink[v] == s.nodeIndex[v] {
		s.sccs = append(s.sccs, s.popSCC(v))
	}
}

// visitNeighbor either recurses into an unvisited node or updates v's lowlink
// from an already-on-stack neighbor — Tarjan's two-arm conditional.
func (s *tarjanState) visitNeighbor(v, w string) {
	if _, visited := s.nodeIndex[w]; !visited {
		s.strongConnect(w)
		if s.lowlink[w] < s.lowlink[v] {
			s.lowlink[v] = s.lowlink[w]
		}
		return
	}
	if s.onStack[w] && s.nodeIndex[w] < s.lowlink[v] {
		s.lowlink[v] = s.nodeIndex[w]
	}
}

// popSCC pops nodes off the stack until v, returning the strongly connected
// component rooted at v.
func (s *tarjanState) popSCC(v string) []string {
	var scc []string
	for {
		w := s.stack[len(s.stack)-1]
		s.stack = s.stack[:len(s.stack)-1]
		s.onStack[w] = false
		scc = append(scc, w)
		if w == v {
			return scc
		}
	}
}

// orphanExemptTypes are node types that are allowed to be unconnected:
// endpoints serve as leaves and infrastructure nodes are pure targets.
var orphanExemptTypes = map[model.NodeType]bool{
	model.NodeEndpoint:    true,
	model.NodeDatabase:    true,
	model.NodeQueue:       true,
	model.NodeCache:       true,
	model.NodeExternalAPI: true,
}

// checkOrphans finds nodes with zero edges (excluding endpoints, which are leaf nodes).
func checkOrphans(graph *model.ArchGraph) []Violation {
	edgeNodes := make(map[string]bool)
	for _, e := range graph.Edges() {
		edgeNodes[e.Source] = true
		edgeNodes[e.Target] = true
	}

	var violations []Violation
	for _, n := range graph.Nodes() {
		if orphanExemptTypes[n.Type] {
			continue
		}
		if edgeNodes[n.ID] {
			continue
		}
		violations = append(violations, Violation{
			Rule:     "no_orphan_nodes",
			Severity: model.SeverityLow,
			Subject:  n.ID,
			Detail:   fmt.Sprintf("Node %q (%s) has no connections", n.Name, n.Type),
		})
	}

	return violations
}

// checkLayeringViolations detects direct edges from endpoints to databases.
func checkLayeringViolations(graph *model.ArchGraph) []Violation {
	var violations []Violation

	for _, e := range graph.Edges() {
		sourceNode := graph.GetNode(e.Source)
		targetNode := graph.GetNode(e.Target)
		if sourceNode == nil || targetNode == nil {
			continue
		}

		if sourceNode.Type == model.NodeEndpoint && targetNode.Type == model.NodeDatabase {
			violations = append(violations, Violation{
				Rule:     "no_endpoint_to_database",
				Severity: model.SeverityMedium,
				Subject:  fmt.Sprintf("%s -> %s", sourceNode.Name, targetNode.Name),
				Detail:   fmt.Sprintf("Endpoint %q directly accesses database %q; consider a service layer", sourceNode.Name, targetNode.Name),
			})
		}
	}

	return violations
}
