package detector

import (
	"fmt"
	"sort"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// Recommendation represents a single architecture improvement suggestion.
type Recommendation struct {
	Category   string     `json:"category"`   // e.g. "break_cycle", "reduce_coupling"
	Priority   string     `json:"priority"`   // "high", "medium", "low"
	Subject    []string   `json:"subject"`    // affected node IDs
	Title      string     `json:"title"`      // one-line summary
	Rationale  string     `json:"rationale"`  // why this matters, with evidence
	Action     string     `json:"action"`     // concrete step to take
	Evidence   []Evidence `json:"evidence"`   // the metric reading(s) that triggered this rec
	Confidence string     `json:"confidence"` // detector certainty: "high", "medium", "low"
}

// Evidence is one falsifiable metric reading behind a recommendation: the value
// observed, the threshold it crossed, and the node it belongs to (empty for
// graph-level signals). Exposing this inline is what makes a recommendation
// verifiable instead of a bare assertion — a consumer or eval can re-derive the
// judgment from the numbers rather than parsing the prose rationale.
type Evidence struct {
	Metric    string  `json:"metric"`            // "instability", "fan_in", "fan_out", "cycle_size", ...
	Value     float64 `json:"value"`             // observed value
	Threshold float64 `json:"threshold"`         // the line it crossed
	Subject   string  `json:"subject,omitempty"` // node ID the reading belongs to
}

// confidenceFromMargin grades detector certainty for threshold-crossing rules by
// how far the observed value exceeds the threshold. Ratio >=1.5 is "high",
// >=1.2 is "medium", otherwise "low" (it barely crossed the line).
func confidenceFromMargin(value, threshold float64) string {
	if threshold <= 0 {
		return "medium"
	}
	switch r := value / threshold; {
	case r >= 1.5:
		return "high"
	case r >= 1.2:
		return "medium"
	default:
		return "low"
	}
}

// weakestConfidence returns the lowest confidence among several signals. A rec
// gated on multiple thresholds is only as certain as its weakest one.
func weakestConfidence(levels ...string) string {
	rank := map[string]int{"high": 0, "medium": 1, "low": 2}
	worst := "high"
	for _, l := range levels {
		if rank[l] > rank[worst] {
			worst = l
		}
	}
	return worst
}

// RecommendArchitecture synthesizes findings from validation, metrics, and
// explanation into prioritized, actionable architecture recommendations.
// Each heuristic rule examines a specific risk surface. After collecting
// all recommendations, priorities are boosted for high-fanin subjects
// and nodes flagged by multiple rules.
func RecommendArchitecture(
	graph *model.ArchGraph,
	violations []Violation,
	metrics *Metrics,
	explanation *Explanation,
) []Recommendation {
	var recs []Recommendation

	recs = append(recs, recommendBreakCycles(graph, violations)...)
	recs = append(recs, recommendReduceCoupling(graph, metrics)...)
	recs = append(recs, recommendStabilizeCore(graph, metrics)...)
	recs = append(recs, recommendAddServiceLayer(graph, violations)...)
	recs = append(recs, recommendSplitDatabase(graph)...)
	recs = append(recs, recommendAddCaching(graph)...)
	recs = append(recs, recommendSplitModule(graph, metrics)...)
	recs = append(recs, recommendRemoveOrphans(graph, violations)...)

	boostPriorities(recs, graph)
	sortRecommendations(recs)

	return recs
}

// recommendBreakCycles suggests breaking circular dependencies found by Tarjan SCC.
func recommendBreakCycles(graph *model.ArchGraph, violations []Violation) []Recommendation {
	if !hasCycleViolation(violations) {
		return nil
	}

	var recs []Recommendation
	for _, scc := range tarjanSCC(graph) {
		if rec, ok := breakCycleRec(scc); ok {
			recs = append(recs, rec)
		}
	}
	return recs
}

// hasCycleViolation reports whether the violations slice contains a cycle rule.
func hasCycleViolation(violations []Violation) bool {
	for _, v := range violations {
		if v.Rule == "no_circular_dependencies" {
			return true
		}
	}
	return false
}

// breakCycleRec builds a break-cycle recommendation for one SCC.
func breakCycleRec(scc []string) (Recommendation, bool) {
	if len(scc) <= 1 {
		return Recommendation{}, false
	}
	return Recommendation{
		Category:  "break_cycle",
		Priority:  "high",
		Subject:   scc,
		Title:     fmt.Sprintf("Break circular dependency among %d components", len(scc)),
		Rationale: fmt.Sprintf("Components %s form a cycle, preventing independent deployment and testing.", strings.Join(scc, ", ")),
		Action:    "Introduce an interface or event-based decoupling to break the dependency loop.",
		// Deterministic (Tarjan SCC): a cycle either exists or it doesn't.
		Confidence: "high",
		Evidence: []Evidence{
			{Metric: "cycle_size", Value: float64(len(scc)), Threshold: 1},
		},
	}, true
}

// recommendReduceCoupling flags nodes with coupling more than 2× the average,
// but only when the average itself exceeds 2 (avoids noise on small graphs).
func recommendReduceCoupling(graph *model.ArchGraph, metrics *Metrics) []Recommendation {
	if metrics == nil || metrics.AvgCoupling <= 2 {
		return nil
	}

	threshold := metrics.AvgCoupling * 2
	var recs []Recommendation
	for _, n := range graph.Nodes() {
		if rec, ok := reduceCouplingRec(n, metrics, threshold); ok {
			recs = append(recs, rec)
		}
	}
	return recs
}

// reduceCouplingRec builds a reduce-coupling recommendation for one node when its
// coupling exceeds the threshold. Returns false for infra nodes or below-threshold.
func reduceCouplingRec(n *model.Node, metrics *Metrics, threshold float64) (Recommendation, bool) {
	if isInfraNode(n.Type) {
		return Recommendation{}, false
	}
	coupling := metrics.Coupling[n.ID]
	if coupling <= threshold {
		return Recommendation{}, false
	}
	priority := "medium"
	if coupling > threshold*1.5 {
		priority = "high"
	}
	return Recommendation{
		Category:   "reduce_coupling",
		Priority:   priority,
		Subject:    []string{n.ID},
		Title:      fmt.Sprintf("Reduce coupling of %s (fan-out %.0f, avg %.1f)", n.Name, coupling, metrics.AvgCoupling),
		Rationale:  fmt.Sprintf("Component %q has %.0f outgoing dependencies, more than 2× the project average of %.1f.", n.Name, coupling, metrics.AvgCoupling),
		Action:     "Extract shared dependencies into a facade or split into smaller focused modules.",
		Confidence: confidenceFromMargin(coupling, threshold),
		Evidence: []Evidence{
			{Metric: "fan_out", Value: coupling, Threshold: threshold, Subject: n.ID},
		},
	}, true
}

// recommendStabilizeCore flags nodes that are heavily depended upon (fan-in > 3)
// yet have high instability (> 0.7). Stable Dependency Principle: depended-upon
// components should be stable.
func recommendStabilizeCore(graph *model.ArchGraph, metrics *Metrics) []Recommendation {
	if metrics == nil {
		return nil
	}

	fanIn := buildFanIn(graph)
	var recs []Recommendation
	for _, n := range graph.Nodes() {
		if rec, ok := stabilizeCoreRec(n, metrics, fanIn); ok {
			recs = append(recs, rec)
		}
	}
	return recs
}

// stabilizeCoreRec builds a stabilize-core recommendation for one node when it has
// high fan-in and high instability. Returns false for infra nodes or below threshold.
func stabilizeCoreRec(n *model.Node, metrics *Metrics, fanIn map[string]int) (Recommendation, bool) {
	if isInfraNode(n.Type) {
		return Recommendation{}, false
	}
	fi := fanIn[n.ID]
	inst := metrics.Instability[n.ID]
	if fi <= 3 || inst <= 0.7 {
		return Recommendation{}, false
	}
	return Recommendation{
		Category:  "stabilize_core",
		Priority:  "high",
		Subject:   []string{n.ID},
		Title:     fmt.Sprintf("Stabilize %s (fan-in %d, instability %.2f)", n.Name, fi, inst),
		Rationale: fmt.Sprintf("Component %q is depended on by %d others but has instability %.2f. Changes here ripple widely.", n.Name, fi, inst),
		Action:    "Reduce outgoing dependencies or extract a stable interface that dependents can rely on.",
		// Gated on two thresholds; only as certain as the weaker margin.
		Confidence: weakestConfidence(
			confidenceFromMargin(float64(fi), 3),
			confidenceFromMargin(inst, 0.7),
		),
		Evidence: []Evidence{
			{Metric: "fan_in", Value: float64(fi), Threshold: 3, Subject: n.ID},
			{Metric: "instability", Value: inst, Threshold: 0.7, Subject: n.ID},
		},
	}, true
}

// recommendAddServiceLayer detects endpoint-to-database layering violations.
func recommendAddServiceLayer(_ *model.ArchGraph, violations []Violation) []Recommendation {
	var recs []Recommendation
	for _, v := range violations {
		if v.Rule != "no_endpoint_to_database" {
			continue
		}
		recs = append(recs, Recommendation{
			Category:  "add_layer",
			Priority:  "medium",
			Subject:   []string{v.Subject},
			Title:     fmt.Sprintf("Add service layer between %s", v.Subject),
			Rationale: fmt.Sprintf("Direct endpoint-to-database access detected: %s. This bypasses business logic and makes changes harder.", v.Detail),
			Action:    "Introduce a service or repository layer to mediate between the endpoint and the database.",
			// Deterministic: a direct endpoint→database edge was found in the graph.
			Confidence: "high",
			Evidence: []Evidence{
				{Metric: "endpoint_to_database_edges", Value: 1, Threshold: 1, Subject: v.Subject},
			},
		})
	}
	return recs
}

// recommendSplitDatabase flags a single shared database accessed by more than 2 services.
func recommendSplitDatabase(graph *model.ArchGraph) []Recommendation {
	databases := graph.NodesByType(model.NodeDatabase)
	if len(databases) != 1 {
		return nil
	}

	services := graph.NodesByType(model.NodeService)
	modules := graph.NodesByType(model.NodeModule)
	serviceCount := len(services) + len(modules)
	if serviceCount <= 2 {
		return nil
	}

	return []Recommendation{
		{
			Category:  "split_database",
			Priority:  "medium",
			Subject:   []string{databases[0].ID},
			Title:     fmt.Sprintf("Consider splitting shared database %s", databases[0].Name),
			Rationale: fmt.Sprintf("Single database %q is shared by %d services. Schema changes affect all consumers and create deployment coupling.", databases[0].Name, serviceCount),
			Action:    "Evaluate database-per-service or schema separation to reduce cross-service coupling.",
			// Heuristic (a shared DB can be intentional): certainty scales with fan-out.
			Confidence: confidenceFromMargin(float64(serviceCount), 2),
			Evidence: []Evidence{
				{Metric: "services_sharing_database", Value: float64(serviceCount), Threshold: 2, Subject: databases[0].ID},
			},
		},
	}
}

// recommendAddCaching flags projects with many endpoints but no cache infrastructure.
func recommendAddCaching(graph *model.ArchGraph) []Recommendation {
	endpoints := graph.NodesByType(model.NodeEndpoint)
	caches := graph.NodesByType(model.NodeCache)
	if len(endpoints) <= 5 || len(caches) > 0 {
		return nil
	}

	return []Recommendation{
		{
			Category:  "add_caching",
			Priority:  "low",
			Subject:   []string{"infrastructure"},
			Title:     fmt.Sprintf("Add caching layer (%d endpoints, no cache detected)", len(endpoints)),
			Rationale: fmt.Sprintf("Found %d HTTP endpoints but no caching infrastructure. Read-heavy endpoints may benefit from caching.", len(endpoints)),
			Action:    "Identify read-heavy endpoints and add a cache (Redis, in-memory) to reduce database load.",
			// Heuristic (not every endpoint set needs a cache): certainty scales with endpoint count.
			Confidence: confidenceFromMargin(float64(len(endpoints)), 5),
			Evidence: []Evidence{
				{Metric: "endpoints", Value: float64(len(endpoints)), Threshold: 5},
				{Metric: "caches", Value: float64(len(caches)), Threshold: 1},
			},
		},
	}
}

// recommendSplitModule flags components with fan-out > 8, suggesting they do too much.
func recommendSplitModule(graph *model.ArchGraph, metrics *Metrics) []Recommendation {
	if metrics == nil {
		return nil
	}

	var recs []Recommendation
	for _, n := range graph.Nodes() {
		if isInfraNode(n.Type) {
			continue
		}
		coupling := metrics.Coupling[n.ID]
		if coupling > 8 {
			recs = append(recs, Recommendation{
				Category:   "split_module",
				Priority:   "medium",
				Subject:    []string{n.ID},
				Title:      fmt.Sprintf("Split %s (fan-out %.0f)", n.Name, coupling),
				Rationale:  fmt.Sprintf("Component %q depends on %.0f other components, suggesting it handles too many responsibilities.", n.Name, coupling),
				Action:     "Decompose into smaller modules, each with a focused responsibility and fewer dependencies.",
				Confidence: confidenceFromMargin(coupling, 8),
				Evidence: []Evidence{
					{Metric: "fan_out", Value: coupling, Threshold: 8, Subject: n.ID},
				},
			})
		}
	}
	return recs
}

// recommendRemoveOrphans flags disconnected non-infrastructure nodes.
func recommendRemoveOrphans(_ *model.ArchGraph, violations []Violation) []Recommendation {
	var recs []Recommendation
	for _, v := range violations {
		if v.Rule != "no_orphan_nodes" {
			continue
		}
		recs = append(recs, Recommendation{
			Category:  "remove_orphan",
			Priority:  "low",
			Subject:   []string{v.Subject},
			Title:     fmt.Sprintf("Remove or connect orphan %s", v.Subject),
			Rationale: v.Detail,
			Action:    "If this component is unused, remove it. If it should be connected, add the missing dependency.",
			// Deterministic: an orphan has zero edges, below the 1 a connected node needs.
			Confidence: "high",
			Evidence: []Evidence{
				{Metric: "connected_edges", Value: 0, Threshold: 1, Subject: v.Subject},
			},
		})
	}
	return recs
}

// boostPriorities upgrades medium→high for nodes that have high fan-in (many
// dependents affected) or are flagged by multiple rules (compound risk).
func boostPriorities(recs []Recommendation, graph *model.ArchGraph) {
	fanIn := buildFanIn(graph)
	subjectCount := countSubjectFlags(recs)

	for i := range recs {
		if recs[i].Priority != "medium" {
			continue
		}
		if shouldBoost(recs[i].Subject, fanIn, subjectCount) {
			recs[i].Priority = "high"
		}
	}
}

// buildFanIn counts incoming dependency edges per target node.
func buildFanIn(graph *model.ArchGraph) map[string]int {
	fanIn := make(map[string]int)
	for _, e := range graph.ResolvedEdges() {
		if e.Type == model.EdgeDependency {
			fanIn[e.Target]++
		}
	}
	return fanIn
}

// countSubjectFlags counts how many rules flag each subject node.
func countSubjectFlags(recs []Recommendation) map[string]int {
	subjectCount := make(map[string]int)
	for _, r := range recs {
		for _, s := range r.Subject {
			subjectCount[s]++
		}
	}
	return subjectCount
}

// shouldBoost reports whether any subject has high fan-in or compound risk.
func shouldBoost(subjects []string, fanIn, subjectCount map[string]int) bool {
	for _, s := range subjects {
		if fanIn[s] > 3 || subjectCount[s] > 1 {
			return true
		}
	}
	return false
}

// sortRecommendations orders by priority (high > medium > low), then by
// number of affected subjects descending.
func sortRecommendations(recs []Recommendation) {
	priorityRank := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.SliceStable(recs, func(i, j int) bool {
		ri, rj := priorityRank[recs[i].Priority], priorityRank[recs[j].Priority]
		if ri != rj {
			return ri < rj
		}
		return len(recs[i].Subject) > len(recs[j].Subject)
	})
}
