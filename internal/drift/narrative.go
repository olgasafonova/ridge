package drift

import (
	"fmt"
	"sort"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// Narrate produces a natural-language summary of a DiffReport. The output is
// 1 sentence for an empty diff and 2-5 sentences otherwise. No LLM calls;
// the function is deterministic templating built from the structured diff.
func Narrate(report *model.DiffReport) string {
	baseRef := refOrDefault(refOf(report, true), "baseline")
	headRef := refOrDefault(refOf(report, false), "current")

	if report == nil || !report.HasChanges() {
		return fmt.Sprintf("No structural change between %s and %s.", baseRef, headRef)
	}

	groups := classifyDiff(report)

	sentences := []string{
		fmt.Sprintf("Between %s and %s, %s.", baseRef, headRef, dominantMove(groups)),
	}
	sentences = appendNodeSentences(sentences, groups)
	sentences = appendEdgeSentence(sentences, groups)
	sentences = appendValidationOrSeverity(sentences, report, groups.validations)

	return strings.Join(sentences, " ")
}

// diffGroups buckets a DiffReport's entries by category and change kind so the
// narrative builders can speak in counts and slices instead of re-filtering.
type diffGroups struct {
	addedNodes   []model.DiffEntry
	removedNodes []model.DiffEntry
	addedEdges   []model.DiffEntry
	removedEdges []model.DiffEntry
	modified     []model.DiffEntry
	validations  []model.DiffEntry
}

func classifyDiff(report *model.DiffReport) diffGroups {
	added := report.ChangesByType(model.ChangeAdded)
	removed := report.ChangesByType(model.ChangeRemoved)
	return diffGroups{
		addedNodes:   filterCategory(added, "node"),
		removedNodes: filterCategory(removed, "node"),
		addedEdges:   filterCategory(added, "edge"),
		removedEdges: filterCategory(removed, "edge"),
		modified:     report.ChangesByType(model.ChangeModified),
		validations:  filterCategory(report.Changes, "validation"),
	}
}

func appendNodeSentences(sentences []string, g diffGroups) []string {
	if len(g.addedNodes) > 0 {
		sentences = append(sentences, capitalize(formatNodeGroup("added", g.addedNodes))+".")
	}
	if len(g.removedNodes) > 0 {
		sentences = append(sentences, capitalize(formatNodeGroup("removed", g.removedNodes))+".")
	}
	return sentences
}

func appendEdgeSentence(sentences []string, g diffGroups) []string {
	if len(g.addedEdges) == 0 && len(g.removedEdges) == 0 {
		return sentences
	}
	return append(sentences, capitalize(formatEdgeChange(len(g.addedEdges), len(g.removedEdges)))+".")
}

func appendValidationOrSeverity(sentences []string, report *model.DiffReport, validations []model.DiffEntry) []string {
	if len(validations) > 0 {
		details := make([]string, 0, len(validations))
		for _, v := range validations {
			details = append(details, v.Detail)
		}
		return append(sentences, "Validation: "+strings.Join(details, "; ")+".")
	}
	if report.MaxSeverity == model.SeverityHigh {
		return append(sentences, "Overall severity: high.")
	}
	return sentences
}

func refOf(report *model.DiffReport, base bool) string {
	if report == nil {
		return ""
	}
	if base {
		return report.BaseRef
	}
	return report.CompareRef
}

func refOrDefault(ref, def string) string {
	if ref == "" {
		return def
	}
	return ref
}

// dominantMove picks the highest-impact phrase to lead the paragraph with.
// Validation issues outrank node changes, which outrank edge changes.
func dominantMove(g diffGroups) string {
	totalNodes := len(g.addedNodes) + len(g.removedNodes)
	totalEdges := len(g.addedEdges) + len(g.removedEdges)

	switch {
	case totalNodes > 0:
		return strings.Join(nodeMoveParts(g, totalEdges), ", ")
	case totalEdges > 0:
		return fmt.Sprintf("%d dependency %s changed", totalEdges, pluralize("edge", totalEdges))
	case len(g.modified) > 0:
		return fmt.Sprintf("%d %s modified in place", len(g.modified), pluralize("component", len(g.modified)))
	}
	return "structural changes detected"
}

// nodeMoveParts builds the comma-joined fragments for the leading sentence
// when nodes are the dominant change.
func nodeMoveParts(g diffGroups, totalEdges int) []string {
	parts := make([]string, 0, 4)
	if len(g.addedNodes) > 0 {
		parts = append(parts, fmt.Sprintf("%d %s added", len(g.addedNodes), pluralize("component", len(g.addedNodes))))
	}
	if len(g.removedNodes) > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", len(g.removedNodes)))
	}
	if len(g.modified) > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", len(g.modified)))
	}
	if totalEdges > 0 {
		parts = append(parts, fmt.Sprintf("%d dependency %s changed", totalEdges, pluralize("edge", totalEdges)))
	}
	return parts
}

// formatNodeGroup builds a phrase like "added 2 packages (foo, bar) and 1 database (postgres)".
func formatNodeGroup(verb string, entries []model.DiffEntry) string {
	if len(entries) == 0 {
		return ""
	}
	byType := map[string][]string{}
	var typeOrder []string
	for _, e := range entries {
		typ, name := nodeTypeAndName(e.Subject)
		if _, ok := byType[typ]; !ok {
			typeOrder = append(typeOrder, typ)
		}
		byType[typ] = append(byType[typ], name)
	}
	sort.Strings(typeOrder)

	phrases := make([]string, 0, len(typeOrder))
	for _, typ := range typeOrder {
		names := byType[typ]
		sort.Strings(names)
		label := pluralizeType(typ, len(names))
		phrases = append(phrases, fmt.Sprintf("%d %s (%s)", len(names), label, joinTrimmed(names, 3)))
	}
	return verb + " " + joinPhrases(phrases)
}

func formatEdgeChange(added, removed int) string {
	switch {
	case added > 0 && removed > 0:
		return fmt.Sprintf("dependency edges shifted: %d added and %d removed", added, removed)
	case added > 0:
		return fmt.Sprintf("added %d new dependency %s", added, pluralize("edge", added))
	case removed > 0:
		return fmt.Sprintf("removed %d dependency %s", removed, pluralize("edge", removed))
	}
	return ""
}

// nodeTypeAndName splits a node ID like "pkg:internal/foo" into ("pkg", "internal/foo").
// IDs without a colon are treated as opaque names with type "node".
func nodeTypeAndName(subject string) (typ, name string) {
	if i := strings.Index(subject, ":"); i > 0 {
		return subject[:i], subject[i+1:]
	}
	return "node", subject
}

func filterCategory(entries []model.DiffEntry, category string) []model.DiffEntry {
	out := make([]model.DiffEntry, 0, len(entries))
	for _, e := range entries {
		if e.Category == category {
			out = append(out, e)
		}
	}
	return out
}

func joinTrimmed(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:max], ", ") + fmt.Sprintf(", and %d more", len(items)-max)
}

func joinPhrases(phrases []string) string {
	switch len(phrases) {
	case 0:
		return ""
	case 1:
		return phrases[0]
	case 2:
		return phrases[0] + " and " + phrases[1]
	}
	return strings.Join(phrases[:len(phrases)-1], ", ") + ", and " + phrases[len(phrases)-1]
}

func pluralize(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// typeNouns maps a node-ID prefix to its (singular, plural) display nouns.
// Unknown prefixes fall back to ("node", "nodes").
var typeNouns = map[string][2]string{
	"pkg":          {"package", "packages"},
	"svc":          {"service", "services"},
	"service":      {"service", "services"},
	"module":       {"module", "modules"},
	"db":           {"database", "databases"},
	"database":     {"database", "databases"},
	"queue":        {"queue", "queues"},
	"cache":        {"cache", "caches"},
	"infra":        {"infrastructure component", "infrastructure components"},
	"api":          {"external API", "external APIs"},
	"external_api": {"external API", "external APIs"},
	"endpoint":     {"endpoint", "endpoints"},
	"note":         {"note", "notes"},
}

// pluralizeType maps ID prefixes to friendly nouns for narrative output.
func pluralizeType(prefix string, n int) string {
	pair, ok := typeNouns[prefix]
	if !ok {
		pair = [2]string{"node", "nodes"}
	}
	if n == 1 {
		return pair[0]
	}
	return pair[1]
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
