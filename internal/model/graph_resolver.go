package model

import (
	"sort"
	"strings"
)

// ResolvedEdges returns edges with import:/wikilink:/route: targets rewritten
// to the concrete node IDs they reference. Unresolvable edges are dropped.
func (g *ArchGraph) ResolvedEdges() []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()

	resolver := newTargetResolver(g.nodes, g.RootPath)
	routes := newRouteResolver(g.nodes)
	seen := make(map[string]bool)
	var result []*Edge

	for _, e := range g.edges {
		target, ok := routes.resolve(e.Target)
		if !ok {
			continue
		}
		target, ok = resolver.resolve(target)
		if !ok {
			continue
		}
		if _, exists := g.nodes[target]; !exists {
			continue
		}
		if e.Source == target {
			continue
		}

		key := e.Source + "|" + target + "|" + string(e.Type)
		if seen[key] {
			continue
		}
		seen[key] = true

		result = append(result, &Edge{
			Source:     e.Source,
			Target:     target,
			Type:       e.Type,
			Label:      e.Label,
			Confidence: confidenceFor(e),
		})
	}
	return result
}

// confidenceFor applies the import-resolution penalty to edge confidence,
// flooring at 0.5. Non-import targets keep their original confidence.
func confidenceFor(e *Edge) float64 {
	if e.Confidence <= 0 || !strings.HasPrefix(e.Target, "import:") {
		return e.Confidence
	}
	conf := e.Confidence - 0.1
	if conf < 0.5 {
		return 0.5
	}
	return conf
}

// relPath is a path relative to the project root. Distinct from a generic
// string so the resolver's signatures don't read as primitive-obsessed.
type relPath string

// rootPrefix is the absolute path prefix that newTargetResolver strips off
// node paths to derive their relPath.
type rootPrefix string

// targetResolver maps edge targets with "import:" / "wikilink:" prefixes
// to concrete node IDs by matching against node Path suffixes. When several
// nodes match, the closest match wins and the lowest node ID breaks a tie, so
// the same graph always resolves the same way.
type targetResolver struct {
	refs          []pathRef
	importCache   map[relPath]string // "" = unresolvable
	wikilinkCache map[relPath]string
}

type pathRef struct {
	rel relPath
	id  string
}

// matchFunc scores how well key resolves to ref. 0 means no match; among
// matches the highest score wins.
type matchFunc func(key relPath, ref pathRef) int

func newTargetResolver(nodes map[string]*Node, root string) *targetResolver {
	prefix := newRootPrefix(root)
	refs := make([]pathRef, 0, len(nodes))
	for _, n := range nodes {
		if ref, ok := nodeRef(n, prefix); ok {
			refs = append(refs, ref)
		}
	}
	// Map iteration order is random; equal scores go to the lowest ID.
	sort.Slice(refs, func(i, j int) bool { return refs[i].id < refs[j].id })
	return &targetResolver{
		refs:          refs,
		importCache:   make(map[relPath]string),
		wikilinkCache: make(map[relPath]string),
	}
}

func newRootPrefix(root string) rootPrefix {
	if root == "" {
		return ""
	}
	return rootPrefix(root + "/")
}

func nodeRef(n *Node, prefix rootPrefix) (pathRef, bool) {
	if n.Path == "" {
		return pathRef{}, false
	}
	rel := n.Path
	if prefix != "" {
		if stripped, ok := strings.CutPrefix(n.Path, string(prefix)); ok {
			rel = stripped
		}
	}
	return pathRef{rel: relPath(rel), id: n.ID}, true
}

// resolve returns (resolvedID, ok). Unprefixed targets pass through unchanged.
func (r *targetResolver) resolve(target string) (string, bool) {
	if path, ok := strings.CutPrefix(target, "import:"); ok {
		return r.lookup(relPath(path), r.importCache, importScore)
	}
	if name, ok := strings.CutPrefix(target, "wikilink:"); ok {
		return r.lookup(relPath(name), r.wikilinkCache, wikilinkScore)
	}
	return target, true
}

func (r *targetResolver) lookup(key relPath, cache map[relPath]string, score matchFunc) (string, bool) {
	if id, cached := cache[key]; cached {
		return id, id != ""
	}
	bestID, best := "", 0
	for _, ref := range r.refs {
		if s := score(key, ref); s > best {
			bestID, best = ref.id, s
		}
	}
	cache[key] = bestID
	return bestID, bestID != ""
}

// importScore matches an import path against a node path that equals it or is
// its tail ("github.com/acme/app/internal/db" against "internal/db"). The
// longest tail wins: "internal/db" over a top-level "db".
func importScore(key relPath, ref pathRef) int {
	if key == ref.rel || strings.HasSuffix(string(key), "/"+string(ref.rel)) {
		return len(ref.rel) + 1
	}
	return 0
}

// Wikilink scores. A tail match loses one point per folder the note sits
// in, so of several notes with the same name the one closest to the root wins.
const (
	wikilinkFullPath = 2_000_000 // the link names the note's whole path
	wikilinkPathTail = 1_000_000 // the link names the end of the path, or the file only
)

// wikilinkScore matches a wikilink against a note path, with or without its
// folders: [[note]] and [[folder/note]] both find "docs/folder/note.md".
func wikilinkScore(key relPath, ref pathRef) int {
	stem := strings.TrimSuffix(string(ref.rel), ".md")
	stem = strings.TrimSuffix(stem, ".markdown")
	target := string(key)
	switch {
	case stem == target:
		return wikilinkFullPath
	case strings.HasSuffix(stem, "/"+target):
		return wikilinkPathTail - strings.Count(stem, "/")
	}
	return 0
}
