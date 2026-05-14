package detector

import (
	"fmt"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// Explanation holds structured architecture analysis.
type Explanation struct {
	Summary        string   `json:"summary"`
	TopologyReason string   `json:"topology_reason"`
	Patterns       []string `json:"patterns"`
	KeyDecisions   []string `json:"key_decisions"`
	Risks          []string `json:"risks"`
}

// ExplainArchitecture provides rich structural analysis of a graph.
func ExplainArchitecture(graph *model.ArchGraph, boundaries *BoundaryResult) *Explanation {
	exp := &Explanation{
		Summary: graph.Summary(),
	}

	exp.TopologyReason = explainTopology(boundaries)
	exp.Patterns = detectPatterns(graph)
	exp.KeyDecisions = extractDecisions(graph)
	exp.Risks = identifyRisks(graph)

	return exp
}

func explainTopology(boundaries *BoundaryResult) string {
	if boundaries == nil {
		return "No boundary information available."
	}

	switch boundaries.Topology {
	case model.TopologyMonolith:
		return "Classified as monolith: single service boundary detected with one module root."
	case model.TopologyMonorepo:
		markers := collectMarkers(boundaries)
		return fmt.Sprintf("Classified as monorepo: multiple module roots detected. Markers: %s.", strings.Join(markers, ", "))
	case model.TopologyMicroservice:
		return fmt.Sprintf("Classified as microservices: %d service boundaries with container/orchestration markers.", len(boundaries.Boundaries))
	default:
		return "Topology could not be determined from project structure."
	}
}

func collectMarkers(boundaries *BoundaryResult) []string {
	seen := make(map[string]bool)
	var markers []string
	for _, b := range boundaries.Boundaries {
		for _, m := range b.Markers {
			if !seen[m] {
				seen[m] = true
				markers = append(markers, m)
			}
		}
	}
	return markers
}

func detectPatterns(graph *model.ArchGraph) []string {
	var patterns []string
	patterns = appendIfNonEmpty(patterns, databasePattern(graph))
	patterns = appendIfNonEmpty(patterns, queuePattern(graph))
	patterns = appendIfNonEmpty(patterns, apiCallPattern(graph))
	patterns = appendIfNonEmpty(patterns, cachePattern(graph))
	patterns = appendIfNonEmpty(patterns, endpointPattern(graph))

	if len(patterns) == 0 {
		patterns = append(patterns, "No distinctive architectural patterns detected")
	}
	return patterns
}

func appendIfNonEmpty(patterns []string, p string) []string {
	if p == "" {
		return patterns
	}
	return append(patterns, p)
}

func databasePattern(graph *model.ArchGraph) string {
	dbCount := len(graph.NodesByType(model.NodeDatabase))
	serviceCount := len(graph.NodesByType(model.NodeService)) + len(graph.NodesByType(model.NodeModule))
	if serviceCount <= 1 {
		return ""
	}
	switch {
	case dbCount == 1:
		return "Shared database: multiple services reference a single data store"
	case dbCount > 1:
		return "Database-per-service: multiple data stores detected alongside multiple services"
	}
	return ""
}

func queuePattern(graph *model.ArchGraph) string {
	if len(graph.NodesByType(model.NodeQueue)) == 0 {
		return ""
	}
	return "Event-driven communication: message queue infrastructure detected"
}

func apiCallPattern(graph *model.ArchGraph) string {
	apiCallCount := 0
	for _, e := range graph.Edges() {
		if e.Type == model.EdgeAPICall && e.Label != "serves" {
			apiCallCount++
		}
	}
	if apiCallCount == 0 {
		return ""
	}
	return fmt.Sprintf("Synchronous inter-service communication: %d API call edges detected", apiCallCount)
}

func cachePattern(graph *model.ArchGraph) string {
	if len(graph.NodesByType(model.NodeCache)) == 0 {
		return ""
	}
	return "Caching layer detected"
}

func endpointPattern(graph *model.ArchGraph) string {
	endpoints := graph.NodesByType(model.NodeEndpoint)
	if len(endpoints) == 0 {
		return ""
	}
	return fmt.Sprintf("HTTP API surface: %d endpoints detected", len(endpoints))
}

func extractDecisions(graph *model.ArchGraph) []string {
	decisions := infraDecisions(graph)
	if d := languageMixDecision(graph); d != "" {
		decisions = append(decisions, d)
	}
	return decisions
}

func infraDecisions(graph *model.ArchGraph) []string {
	var decisions []string
	for _, n := range graph.Nodes() {
		if d, ok := infraDecisionFor(n); ok {
			decisions = append(decisions, d)
		}
	}
	return decisions
}

func infraDecisionFor(n *model.Node) (string, bool) {
	detectedVia, ok := n.Properties["detected_via"]
	if !ok {
		return "", false
	}
	switch n.Type {
	case model.NodeDatabase:
		return fmt.Sprintf("Uses %s for data persistence (detected via %s import)", n.Name, detectedVia), true
	case model.NodeQueue:
		return fmt.Sprintf("Uses %s for messaging (detected via %s import)", n.Name, detectedVia), true
	case model.NodeCache:
		return fmt.Sprintf("Uses %s for caching (detected via %s import)", n.Name, detectedVia), true
	case model.NodeExternalAPI:
		return fmt.Sprintf("Calls external APIs (detected via %s import)", detectedVia), true
	}
	return "", false
}

func languageMixDecision(graph *model.ArchGraph) string {
	languages := make(map[string]int)
	for _, n := range graph.Nodes() {
		if n.Language != "" {
			languages[n.Language]++
		}
	}
	switch len(languages) {
	case 0:
		return ""
	case 1:
		for lang := range languages {
			return fmt.Sprintf("Single-language codebase: %s", lang)
		}
		return ""
	default:
		var langParts []string
		for lang, count := range languages {
			langParts = append(langParts, fmt.Sprintf("%s (%d components)", lang, count))
		}
		return fmt.Sprintf("Multi-language codebase: %s", strings.Join(langParts, ", "))
	}
}

func identifyRisks(graph *model.ArchGraph) []string {
	var risks []string
	risks = appendIfNonEmpty(risks, sharedDatabaseRisk(graph))
	risks = appendIfNonEmpty(risks, missingCacheRisk(graph))
	risks = appendIfNonEmpty(risks, cycleRisk(graph))
	risks = appendIfNonEmpty(risks, orphanRisk(graph))
	return risks
}

func sharedDatabaseRisk(graph *model.ArchGraph) string {
	dbCount := len(graph.NodesByType(model.NodeDatabase))
	serviceCount := len(graph.NodesByType(model.NodeService)) + len(graph.NodesByType(model.NodeModule))
	if dbCount == 1 && serviceCount > 2 {
		return "Single database shared by multiple services may create coupling and scaling bottlenecks"
	}
	return ""
}

func missingCacheRisk(graph *model.ArchGraph) string {
	endpoints := graph.NodesByType(model.NodeEndpoint)
	caches := graph.NodesByType(model.NodeCache)
	if len(endpoints) > 5 && len(caches) == 0 {
		return "No caching layer detected despite multiple endpoints; may impact performance under load"
	}
	return ""
}

func cycleRisk(graph *model.ArchGraph) string {
	if graph.HasCycle() {
		return "Circular dependencies detected; may cause build issues and unclear ownership"
	}
	return ""
}

func orphanRisk(graph *model.ArchGraph) string {
	edgeNodes := make(map[string]bool)
	for _, e := range graph.Edges() {
		edgeNodes[e.Source] = true
		edgeNodes[e.Target] = true
	}
	orphanCount := 0
	for _, n := range graph.Nodes() {
		if n.Type != model.NodeEndpoint && !edgeNodes[n.ID] {
			orphanCount++
		}
	}
	if orphanCount == 0 {
		return ""
	}
	return fmt.Sprintf("%d disconnected nodes detected; may indicate dead code or missing integrations", orphanCount)
}
