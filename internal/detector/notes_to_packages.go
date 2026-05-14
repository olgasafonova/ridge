package detector

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// LinkNotesToPackages is a spike implementation of cross-substrate edges:
// notes (markdown) → Go packages, by matching path-like tokens inside inline
// code spans against package node directories.
//
// Status: exploration. Not wired into any MCP tool. See
// docs/cross-substrate-exploration.md for the design rationale, open
// questions, and the path to graduating this into a real primitive.
//
// Match policy:
//   - Only inline code spans (single backticks `...`) are considered. Plain
//     prose is too noisy for the first cut.
//   - A candidate matches a package if a trailing path-suffix of the package's
//     directory (after `internal/`-style root stripping) equals the candidate.
//   - Trailing slashes, `:line[-line]` ranges, and `.go`/`.ts`/`.py` file
//     extensions on candidates are normalized away.
//   - Confidence: 0.6 (heuristic; AST-resolved imports score 0.9).
//
// LinkNotesToPackages mutates graph in place and returns the count of edges
// added. Callers may run this after Scan completes.
func LinkNotesToPackages(graph *model.ArchGraph) int {
	notes, packagesBySuffix := collectNotesAndPackages(graph)
	if len(notes) == 0 || len(packagesBySuffix) == 0 {
		return 0
	}

	added := 0
	for _, note := range notes {
		added += linkOneNote(graph, note, packagesBySuffix)
	}
	return added
}

// linkOneNote reads a single note file and adds "documents" edges from the
// note to every package whose path-suffix matches a code-span candidate.
// Returns the number of edges actually added.
func linkOneNote(graph *model.ArchGraph, note *model.Node, packagesBySuffix map[string][]*model.Node) int {
	src, err := os.ReadFile(note.Path) //nolint:gosec // G304: path from analyzer-emitted node
	if err != nil {
		return 0
	}
	added := 0
	seen := map[string]bool{}
	for _, m := range inlineCodeSpanRe.FindAllStringSubmatch(string(src), -1) {
		candidate := normalizeCandidate(m[1])
		if candidate == "" {
			continue
		}
		added += linkNoteToCandidates(graph, note, packagesBySuffix[candidate], seen)
	}
	return added
}

// linkNoteToCandidates emits one "documents" edge per (note, pkg) pair that
// hasn't already been emitted in this note's pass.
func linkNoteToCandidates(graph *model.ArchGraph, note *model.Node, candidates []*model.Node, seen map[string]bool) int {
	added := 0
	for _, pkg := range candidates {
		key := note.ID + "->" + pkg.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		if graph.AddEdge(&model.Edge{
			Source:     note.ID,
			Target:     pkg.ID,
			Type:       model.EdgeDependency,
			Label:      "documents",
			Confidence: 0.6,
		}) {
			added++
		}
	}
	return added
}

// inlineCodeSpanRe matches single-backtick inline code spans on one line.
// Multi-line and fenced blocks are intentionally ignored.
var inlineCodeSpanRe = regexp.MustCompile("`([^`\\n]+?)`")

// pathLikeRe filters candidates down to identifier paths.
var pathLikeRe = regexp.MustCompile(`^[a-zA-Z_][\w./-]*$`)

// lineRangeRe strips trailing :42 or :42-50 line references.
var lineRangeRe = regexp.MustCompile(`:\d+(-\d+)?$`)

// normalizeCandidate trims a code-span candidate into a comparable path.
// Returns "" if the candidate isn't path-shaped.
func normalizeCandidate(s string) string {
	s = strings.TrimSpace(s)
	s = lineRangeRe.ReplaceAllString(s, "")
	s = strings.TrimRight(s, "/")
	if !strings.Contains(s, "/") {
		return ""
	}
	if !pathLikeRe.MatchString(s) {
		return ""
	}
	// If the last segment looks like a source file, drop it so we match the
	// containing package.
	last := filepath.Base(s)
	switch filepath.Ext(last) {
	case ".go", ".ts", ".tsx", ".py", ".js":
		s = filepath.Dir(s)
	}
	if !strings.Contains(s, "/") {
		return ""
	}
	return s
}

// collectNotesAndPackages walks the graph once, returning all note nodes plus
// an index of packages keyed by all path suffixes of length >= 2.
func collectNotesAndPackages(graph *model.ArchGraph) ([]*model.Node, map[string][]*model.Node) {
	var notes []*model.Node
	pkgsBySuffix := map[string][]*model.Node{}

	for _, n := range graph.Nodes() {
		switch n.Type {
		case model.NodeNote:
			if n.Path != "" {
				notes = append(notes, n)
			}
		case model.NodePackage:
			if n.Path == "" {
				continue
			}
			for _, suf := range pathSuffixes(n.Path) {
				pkgsBySuffix[suf] = append(pkgsBySuffix[suf], n)
			}
		}
	}
	return notes, pkgsBySuffix
}

// pathSuffixes returns trailing path forms of length >= 2 segments.
// Example: "/repo/internal/scanner" → ["internal/scanner", "repo/internal/scanner"].
// We never index the bare leaf segment alone because too many leaves collide
// (many projects have a "server" or "client" package).
func pathSuffixes(path string) []string {
	cleaned := filepath.Clean(path)
	parts := strings.Split(cleaned, string(filepath.Separator))
	// Drop empty leading segment for absolute paths on POSIX.
	for len(parts) > 0 && parts[0] == "" {
		parts = parts[1:]
	}
	if len(parts) < 2 {
		return nil
	}
	out := make([]string, 0, len(parts)-1)
	for i := len(parts) - 2; i >= 0; i-- {
		out = append(out, strings.Join(parts[i:], "/"))
	}
	return out
}
