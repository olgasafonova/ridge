package drift

import (
	"fmt"

	"github.com/olgasafonova/ridge/internal/model"
)

// Compare detects differences between a baseline graph and a current graph.
func Compare(baseline, current *model.ArchGraph) *model.DiffReport {
	report := &model.DiffReport{
		MaxSeverity: model.SeverityNone,
		Changes:     []model.DiffEntry{},
	}

	baseNodes := nodeMap(baseline.Nodes())
	currNodes := nodeMap(current.Nodes())
	diffNodes(report, baseNodes, currNodes)
	diffEdges(report, edgeSet(baseline.Edges()), edgeSet(current.Edges()))
	checkCycle(report, current)
	report.Summary = summarize(report)

	return report
}

// diffNodes records added / removed / modified node entries between two nodemaps.
func diffNodes(report *model.DiffReport, base, curr map[string]*model.Node) {
	recordSideOnlyNodes(report, curr, base, model.ChangeAdded)
	recordSideOnlyNodes(report, base, curr, model.ChangeRemoved)
	recordModifiedNodes(report, base, curr)
}

// recordSideOnlyNodes appends a change entry for each node in have whose ID
// does not appear in other.
func recordSideOnlyNodes(report *model.DiffReport, have, other map[string]*model.Node, change model.DiffChangeType) {
	for id, node := range have {
		if _, exists := other[id]; exists {
			continue
		}
		recordNodeChange(report, change, node, verbFor(change))
		_ = id
	}
}

// recordModifiedNodes appends a Modified entry for each shared ID whose
// (Name, Type) differs between base and curr.
func recordModifiedNodes(report *model.DiffReport, base, curr map[string]*model.Node) {
	for id, currNode := range curr {
		baseNode, exists := base[id]
		if !exists {
			continue
		}
		if nodesEqual(baseNode, currNode) {
			continue
		}
		report.Changes = append(report.Changes, model.DiffEntry{
			ChangeType: model.ChangeModified,
			Severity:   model.SeverityLow,
			Category:   "node",
			Subject:    id,
			Detail:     fmt.Sprintf("Modified %s: %s", currNode.Type, currNode.Name),
		})
		report.MaxSeverity = maxSeverity(report.MaxSeverity, model.SeverityLow)
	}
}

// nodesEqual reports whether two nodes are equivalent for diff-modification
// purposes: same Name and same Type.
func nodesEqual(a, b *model.Node) bool {
	return a.Name == b.Name && a.Type == b.Type
}

// verbFor returns the past-tense English label for a change type, used in
// human-readable diff entry details.
func verbFor(change model.DiffChangeType) string {
	switch change {
	case model.ChangeAdded:
		return "Added"
	case model.ChangeRemoved:
		return "Removed"
	default:
		return "Modified"
	}
}

// recordNodeChange appends an Added/Removed entry for one node and bumps
// MaxSeverity to match the node's classified severity.
func recordNodeChange(report *model.DiffReport, change model.DiffChangeType, node *model.Node, verb string) {
	severity := classifyNodeChangeSeverity(node)
	report.Changes = append(report.Changes, model.DiffEntry{
		ChangeType: change,
		Severity:   severity,
		Category:   "node",
		Subject:    node.ID,
		Detail:     fmt.Sprintf("%s %s: %s (%s)", verb, node.Type, node.Name, node.ID),
	})
	report.MaxSeverity = maxSeverity(report.MaxSeverity, severity)
}

// diffEdges records added / removed edges between two edge-key sets.
func diffEdges(report *model.DiffReport, base, curr map[string]bool) {
	recordSideOnlyEdges(report, curr, base, model.ChangeAdded)
	recordSideOnlyEdges(report, base, curr, model.ChangeRemoved)
}

// recordSideOnlyEdges appends an edge-change entry for each key in have that
// is missing from other.
func recordSideOnlyEdges(report *model.DiffReport, have, other map[string]bool, change model.DiffChangeType) {
	for key := range have {
		if other[key] {
			continue
		}
		recordEdgeChange(report, change, key, verbFor(change))
	}
}

// recordEdgeChange appends an Added/Removed entry for one edge key.
func recordEdgeChange(report *model.DiffReport, change model.DiffChangeType, key, verb string) {
	report.Changes = append(report.Changes, model.DiffEntry{
		ChangeType: change,
		Severity:   model.SeverityMedium,
		Category:   "edge",
		Subject:    key,
		Detail:     fmt.Sprintf("%s edge: %s", verb, key),
	})
	report.MaxSeverity = maxSeverity(report.MaxSeverity, model.SeverityMedium)
}

// checkCycle records a critical-severity entry when the current graph has a
// circular dependency.
func checkCycle(report *model.DiffReport, current *model.ArchGraph) {
	if !current.HasCycle() {
		return
	}
	report.Changes = append(report.Changes, model.DiffEntry{
		ChangeType: model.ChangeAdded,
		Severity:   model.SeverityCritical,
		Category:   "validation",
		Subject:    "circular_dependency",
		Detail:     "Circular dependency detected in current architecture",
	})
	report.MaxSeverity = model.SeverityCritical
}

// summarize builds the human-readable one-line summary for a report.
func summarize(report *model.DiffReport) string {
	if len(report.Changes) == 0 {
		return "No architectural changes detected."
	}
	added := len(report.ChangesByType(model.ChangeAdded))
	removed := len(report.ChangesByType(model.ChangeRemoved))
	modified := len(report.ChangesByType(model.ChangeModified))
	return fmt.Sprintf(
		"%d changes detected: %d added, %d removed, %d modified. Max severity: %s.",
		len(report.Changes), added, removed, modified, report.MaxSeverity,
	)
}

func nodeMap(nodes []*model.Node) map[string]*model.Node {
	m := make(map[string]*model.Node, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n
	}
	return m
}

func edgeSet(edges []*model.Edge) map[string]bool {
	s := make(map[string]bool, len(edges))
	for _, e := range edges {
		key := fmt.Sprintf("%s-[%s]->%s", e.Source, e.Type, e.Target)
		s[key] = true
	}
	return s
}

func classifyNodeChangeSeverity(node *model.Node) model.DiffSeverity {
	switch node.Type {
	case model.NodeService:
		return model.SeverityHigh
	case model.NodeDatabase, model.NodeQueue, model.NodeCache:
		return model.SeverityHigh
	case model.NodeExternalAPI:
		return model.SeverityMedium
	case model.NodeModule, model.NodePackage:
		return model.SeverityMedium
	case model.NodeEndpoint:
		return model.SeverityLow
	default:
		return model.SeverityLow
	}
}

func maxSeverity(a, b model.DiffSeverity) model.DiffSeverity {
	order := map[model.DiffSeverity]int{
		model.SeverityNone:     0,
		model.SeverityLow:      1,
		model.SeverityMedium:   2,
		model.SeverityHigh:     3,
		model.SeverityCritical: 4,
	}
	if order[b] > order[a] {
		return b
	}
	return a
}
