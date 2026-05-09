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

// targetResolver maps edge targets with "import:" / "wikilink:" prefixes
// to concrete node IDs by matching against node Path suffixes.
type targetResolver struct {
	refs          []pathRef
	importCache   map[string]string // "" = unresolvable
	wikilinkCache map[string]string
}

type pathRef struct {
	relPath string
	id      string
}

func newTargetResolver(nodes map[string]*Node, rootPath string) *targetResolver {
	prefix := pathPrefix(rootPath)
	var refs []pathRef
	for _, n := range nodes {
		if ref, ok := nodeRef(n, prefix); ok {
			refs = append(refs, ref)
		}
	}
	return &targetResolver{
		refs:          refs,
		importCache:   make(map[string]string),
		wikilinkCache: make(map[string]string),
	}
}

func pathPrefix(rootPath string) string {
	if rootPath == "" {
		return ""
	}
	return rootPath + "/"
}

func nodeRef(n *Node, prefix string) (pathRef, bool) {
	if n.Path == "" {
		return pathRef{}, false
	}
	relPath := n.Path
	if prefix != "" {
		if rel, ok := strings.CutPrefix(n.Path, prefix); ok {
			relPath = rel
		}
	}
	return pathRef{relPath: relPath, id: n.ID}, true
}

// resolve returns (resolvedID, ok). Unprefixed targets pass through unchanged.
func (r *targetResolver) resolve(target string) (string, bool) {
	if path, ok := strings.CutPrefix(target, "import:"); ok {
		return r.lookup(path, r.importCache, importMatches)
	}
	if name, ok := strings.CutPrefix(target, "wikilink:"); ok {
		return r.lookup(name, r.wikilinkCache, wikilinkMatches)
	}
	return target, true
}

func (r *targetResolver) lookup(key string, cache map[string]string, match func(string, pathRef) bool) (string, bool) {
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

func importMatches(importPath string, ref pathRef) bool {
	return importPath == ref.relPath || strings.HasSuffix(importPath, "/"+ref.relPath)
}

func wikilinkMatches(linkName string, ref pathRef) bool {
	stem := strings.TrimSuffix(ref.relPath, ".md")
	stem = strings.TrimSuffix(stem, ".markdown")
	base := stem
	if i := strings.LastIndex(stem, "/"); i >= 0 {
		base = stem[i+1:]
	}
	return base == linkName || stem == linkName || strings.HasSuffix(stem, "/"+linkName)
}
