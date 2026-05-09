package model

import "strings"

// ResolvedEdges returns edges with import:/wikilink: targets rewritten to the
// concrete node IDs they reference. Unresolvable edges are dropped.
func (g *ArchGraph) ResolvedEdges() []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()

	resolver := newTargetResolver(g.nodes, g.RootPath)
	seen := make(map[string]bool)
	var result []*Edge

	for _, e := range g.edges {
		target, ok := resolver.resolve(e.Target)
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
// to concrete node IDs by matching against node Path suffixes.
type targetResolver struct {
	refs          []pathRef
	importCache   map[relPath]string // "" = unresolvable
	wikilinkCache map[relPath]string
}

type pathRef struct {
	rel relPath
	id  string
}

// matchFunc reports whether key resolves to ref.
type matchFunc func(key relPath, ref pathRef) bool

func newTargetResolver(nodes map[string]*Node, root string) *targetResolver {
	prefix := newRootPrefix(root)
	refs := make([]pathRef, 0, len(nodes))
	for _, n := range nodes {
		if ref, ok := nodeRef(n, prefix); ok {
			refs = append(refs, ref)
		}
	}
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
		return r.lookup(relPath(path), r.importCache, importMatches)
	}
	if name, ok := strings.CutPrefix(target, "wikilink:"); ok {
		return r.lookup(relPath(name), r.wikilinkCache, wikilinkMatches)
	}
	return target, true
}

func (r *targetResolver) lookup(key relPath, cache map[relPath]string, match matchFunc) (string, bool) {
	if id, cached := cache[key]; cached {
		return id, id != ""
	}
	for _, ref := range r.refs {
		if match(key, ref) {
			cache[key] = ref.id
			return ref.id, true
		}
	}
	cache[key] = ""
	return "", false
}

func importMatches(key relPath, ref pathRef) bool {
	return key == ref.rel || strings.HasSuffix(string(key), "/"+string(ref.rel))
}

func wikilinkMatches(key relPath, ref pathRef) bool {
	stem := strings.TrimSuffix(string(ref.rel), ".md")
	stem = strings.TrimSuffix(stem, ".markdown")
	base := stem
	if i := strings.LastIndex(stem, "/"); i >= 0 {
		base = stem[i+1:]
	}
	target := string(key)
	return base == target || stem == target || strings.HasSuffix(stem, "/"+target)
}
