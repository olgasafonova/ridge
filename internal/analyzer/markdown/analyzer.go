// Package markdown provides markdown analysis for architecture graphs.
// Each .md file becomes a NodeNote; wiki-links and relative markdown links become EdgeDependency edges.
package markdown

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/scanner"
)

// Analyzer implements the scanner.Analyzer interface for markdown files.
type Analyzer struct{}

// New creates a markdown analyzer.
func New() *Analyzer {
	return &Analyzer{}
}

// Extensions returns markdown file extensions.
func (a *Analyzer) Extensions() []string {
	return []string{".md", ".markdown"}
}

// Language returns "markdown".
func (a *Analyzer) Language() string {
	return "markdown"
}

// Clone returns an independent copy. The markdown analyzer is stateless.
func (a *Analyzer) Clone() scanner.Analyzer {
	return New()
}

// mdAnalyzerVersion is the cache-invalidation key for this analyzer. Bump it
// when emitted Nodes/Edges change shape or when link-extraction logic changes.
const mdAnalyzerVersion = "md-v1"

// Signature returns the analyzer's behavior version used for cache invalidation.
func (a *Analyzer) Signature() string {
	return mdAnalyzerVersion
}

var (
	// Wiki-link: [[target]] or [[target|display]] or [[target#section]] or [[target#section|display]].
	// Captures the full inside content; we split on # and | downstream.
	wikiLinkRe = regexp.MustCompile(`\[\[([^\[\]\n]+?)\]\]`)

	// Standard markdown link: [text](target) — target stops at whitespace or close paren.
	// Title attribute "..." after target is tolerated by the trailing optional group.
	mdLinkRe = regexp.MustCompile(`\[([^\]\n]*)\]\(([^\s)]+)(?:\s+"[^"]*")?\)`)

	// Fenced code block: ``` or ~~~ on its own line, possibly with a language tag.
	fenceOpenRe = regexp.MustCompile("(?m)^[ \\t]*(```+|~~~+)[^\\n]*$")

	// nonRelativeMarkdownPrefixes are link prefixes that mean "not a relative
	// markdown file" — external schemes and special protocols.
	nonRelativeMarkdownPrefixes = []string{"mailto:", "tel:"}
)

// maxFileBytes caps how much of any one markdown file we read. Vaults
// occasionally contain pasted full-text articles or image-embedded notes that
// can balloon to multi-MB; reading them entirely into memory and then running
// regexes over them stalls the scanner with no useful link signal in return.
const maxFileBytes = 5 * 1024 * 1024 // 5 MB

// Analyze parses a markdown file and emits a note node + link edges.
func (a *Analyzer) Analyze(path string) ([]*model.Node, []*model.Edge, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat %s: %w", path, err)
	}

	noteID, stem := noteIdentifiers(path)

	if info.Size() > maxFileBytes {
		return []*model.Node{noteSkippedForSize(noteID, stem, path, info.Size())}, nil, nil
	}

	src, err := os.ReadFile(path) //nolint:gosec // G304: path is from filesystem walker, not user input
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}

	stripped := stripInlineCode(stripCodeBlocks(string(src)))

	seen := make(map[string]bool) // dedupe identical link targets within one file
	edges := extractWikiLinks(noteID, stripped, seen)
	edges = append(edges, extractMarkdownLinks(noteID, stripped, seen)...)

	nodes := []*model.Node{{
		ID:       noteID,
		Name:     stem,
		Type:     model.NodeNote,
		Language: "markdown",
		Path:     path,
	}}
	return nodes, edges, nil
}

// noteIdentifiers derives the canonical note ID and display stem from a markdown file path.
func noteIdentifiers(path string) (id, stem string) {
	dir := filepath.Base(filepath.Dir(path))
	stem = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	id = fmt.Sprintf("note:%s/%s", dir, stem)
	return id, stem
}

// noteSkippedForSize builds a note node for a markdown file that exceeds the
// scan size cap. The file is visible in the graph but no link extraction runs.
func noteSkippedForSize(id, stem, path string, size int64) *model.Node {
	return &model.Node{
		ID:       id,
		Name:     stem,
		Type:     model.NodeNote,
		Language: "markdown",
		Path:     path,
		Properties: map[string]string{
			"skipped":      "size",
			"size_bytes":   fmt.Sprintf("%d", size),
			"max_capacity": fmt.Sprintf("%d", maxFileBytes),
		},
	}
}

// extractWikiLinks emits an EdgeDependency for each unique [[target]] / [[target|display]]
// found in stripped. seen is shared across link types so a wiki-link and an md-link to
// the same target dedupe to one edge.
func extractWikiLinks(noteID, stripped string, seen map[string]bool) []*model.Edge {
	var edges []*model.Edge
	for _, m := range wikiLinkRe.FindAllStringSubmatch(stripped, -1) {
		inner := strings.TrimSpace(m[1])
		if inner == "" {
			continue
		}
		linkTarget, display := splitWikiTarget(inner)
		if linkTarget == "" {
			continue
		}
		if !markSeen(seen, "wiki|"+linkTarget) {
			continue
		}
		edges = append(edges, &model.Edge{
			Source:     noteID,
			Target:     "wikilink:" + linkTarget,
			Type:       model.EdgeDependency,
			Label:      display,
			Confidence: 0.9,
		})
	}
	return edges
}

// extractMarkdownLinks emits an EdgeDependency for each unique relative .md link.
// External URLs, mailto:, anchors, and non-markdown targets are skipped.
func extractMarkdownLinks(noteID, stripped string, seen map[string]bool) []*model.Edge {
	var edges []*model.Edge
	for _, m := range mdLinkRe.FindAllStringSubmatch(stripped, -1) {
		display := strings.TrimSpace(m[1])
		raw := strings.TrimSpace(m[2])
		linkTarget, ok := relativeMarkdownTarget(raw)
		if !ok {
			continue
		}
		if !markSeen(seen, "md|"+linkTarget) {
			continue
		}
		edges = append(edges, &model.Edge{
			Source:     noteID,
			Target:     "wikilink:" + linkTarget,
			Type:       model.EdgeDependency,
			Label:      display,
			Confidence: 0.85,
		})
	}
	return edges
}

// markSeen records key in seen and reports whether the caller should proceed
// (true on first sight, false if already seen).
func markSeen(seen map[string]bool, key string) bool {
	if seen[key] {
		return false
	}
	seen[key] = true
	return true
}

// splitWikiTarget parses an Obsidian wiki-link inner string.
// Forms: "name", "name|display", "name#section", "name#section|display", "folder/name".
// Returns the link target (stripped of #section, |display) and the display text if present.
func splitWikiTarget(inner string) (target, display string) {
	if i := strings.Index(inner, "|"); i >= 0 {
		display = strings.TrimSpace(inner[i+1:])
		inner = inner[:i]
	}
	if i := strings.Index(inner, "#"); i >= 0 {
		inner = inner[:i]
	}
	target = strings.TrimSpace(inner)
	if display == "" {
		display = target
	}
	return target, display
}

// relativeMarkdownTarget extracts the basename (without extension) of a relative markdown link.
// Skips external URLs (scheme://...), mailto, same-page anchors, and non-.md targets.
func relativeMarkdownTarget(raw string) (string, bool) {
	if raw == "" || strings.HasPrefix(raw, "#") {
		return "", false
	}
	if isExternalLink(raw) {
		return "", false
	}
	raw = stripFragmentAndQuery(raw)
	if !isMarkdownExtension(raw) {
		return "", false
	}
	stem := strings.TrimSpace(strings.TrimSuffix(filepath.Base(raw), filepath.Ext(raw)))
	if stem == "" {
		return "", false
	}
	return stem, true
}

// isExternalLink reports whether raw points outside the local markdown tree:
// either a scheme-qualified URL or one of the special protocols (mailto:/tel:).
func isExternalLink(raw string) bool {
	if strings.Contains(raw, "://") {
		return true
	}
	for _, prefix := range nonRelativeMarkdownPrefixes {
		if strings.HasPrefix(raw, prefix) {
			return true
		}
	}
	return false
}

// stripFragmentAndQuery returns raw with any "#fragment" or "?query" suffix removed.
func stripFragmentAndQuery(raw string) string {
	if i := strings.IndexAny(raw, "#?"); i >= 0 {
		return raw[:i]
	}
	return raw
}

// isMarkdownExtension reports whether raw ends in .md or .markdown (case-insensitive).
func isMarkdownExtension(raw string) bool {
	ext := strings.ToLower(filepath.Ext(raw))
	return ext == ".md" || ext == ".markdown"
}

// stripCodeBlocks removes fenced code blocks (``` or ~~~) so links inside them are ignored.
// Matching is fence-symbol aware: a ``` block can only be closed by ``` (and ~~~ by ~~~),
// preventing a stray fence inside a code block from prematurely re-opening parsing.
func stripCodeBlocks(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	inBlock := false
	var fenceChar byte
	for _, line := range lines {
		if !inBlock {
			if opened, ch := openingFence(line); opened {
				fenceChar = ch
				inBlock = true
				out = append(out, "")
				continue
			}
			out = append(out, line)
			continue
		}
		if closesFence(line, fenceChar) {
			inBlock = false
		}
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// openingFence reports whether line starts a fenced code block and, if so,
// returns the fence character ('`' or '~').
func openingFence(line string) (bool, byte) {
	if fenceOpenRe.FindStringIndex(line) == nil {
		return false, 0
	}
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return true, 0
	}
	return true, trimmed[0]
}

// closesFence reports whether line closes a code block opened with fenceChar.
// Only the matching fence character can close the block — a stray fence of
// the other kind inside a block does not terminate it.
func closesFence(line string, fenceChar byte) bool {
	trimmed := strings.TrimLeft(line, " \t")
	switch fenceChar {
	case '`':
		return strings.HasPrefix(trimmed, "```")
	case '~':
		return strings.HasPrefix(trimmed, "~~~")
	}
	return false
}

// stripInlineCode replaces backtick-delimited inline code spans with spaces of equal length,
// so links inside `code` are ignored without shifting line/col positions.
func stripInlineCode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] != '`' {
			b.WriteByte(s[i])
			i++
			continue
		}
		runLen, afterOpen := scanBacktickRun(s, i)
		end := findClosingRun(s, afterOpen, runLen)
		if end == -1 {
			b.WriteString(s[i:afterOpen])
			i = afterOpen
			continue
		}
		writeSpaceMask(&b, s, i, end+runLen)
		i = end + runLen
	}
	return b.String()
}

// scanBacktickRun counts a contiguous run of backticks starting at i, returning
// its length and the index just past the run.
func scanBacktickRun(s string, i int) (int, int) {
	j := i
	for j < len(s) && s[j] == '`' {
		j++
	}
	return j - i, j
}

// findClosingRun searches s[from:] for a backtick run of exactly length runLen
// and returns its starting index, or -1 if no matching closing run exists.
func findClosingRun(s string, from, runLen int) int {
	for k := from; k < len(s); {
		if s[k] != '`' {
			k++
			continue
		}
		_, m := scanBacktickRun(s, k)
		if m-k == runLen {
			return k
		}
		k = m
	}
	return -1
}

// writeSpaceMask writes s[start:end] to b with every non-newline character
// replaced by a space, preserving line/column offsets.
func writeSpaceMask(b *strings.Builder, s string, start, end int) {
	for p := start; p < end; p++ {
		if s[p] == '\n' {
			b.WriteByte('\n')
		} else {
			b.WriteByte(' ')
		}
	}
}
