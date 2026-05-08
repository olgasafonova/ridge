// Package model defines the core architecture graph data model.
// ArchGraph is the central type; all analyzers produce Nodes and Edges into it.
package model

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// NodeType classifies architecture components.
type NodeType string

const (
	NodeService     NodeType = "service"
	NodeModule      NodeType = "module"
	NodeDatabase    NodeType = "database"
	NodeQueue       NodeType = "queue"
	NodeCache       NodeType = "cache"
	NodeExternalAPI NodeType = "external_api"
	NodePackage     NodeType = "package"
	NodeEndpoint    NodeType = "endpoint"
	NodeNote        NodeType = "note"
)

// EdgeType classifies relationships between components.
type EdgeType string

const (
	EdgeDependency EdgeType = "dependency"
	EdgeAPICall    EdgeType = "api_call"
	EdgeDataFlow   EdgeType = "data_flow"
	EdgePublish    EdgeType = "publish"
	EdgeSubscribe  EdgeType = "subscribe"
	EdgeReadWrite  EdgeType = "read_write"
)

// Node represents an architecture component.
type Node struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       NodeType          `json:"type"`
	Language   string            `json:"language,omitempty"`
	Path       string            `json:"path,omitempty"`
	Source     string            `json:"source,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
}

// Edge represents a relationship between two nodes.
type Edge struct {
	Source     string            `json:"source"`
	Target     string            `json:"target"`
	Type       EdgeType          `json:"type"`
	Label      string            `json:"label,omitempty"`
	Confidence float64           `json:"confidence"`
	Properties map[string]string `json:"properties,omitempty"`
}

// ProcessTrace represents a data flow chain from an entry point to a terminal node.
type ProcessTrace struct {
	EntryPoint string   `json:"entry_point"`
	Chain      []string `json:"chain"`
	EdgeTypes  []string `json:"edge_types"`
	Terminal   string   `json:"terminal"`
	Confidence float64  `json:"confidence"`
}

// ArchGraph is the central architecture model.
// All analyzers produce Nodes and Edges into the same graph structure.
type ArchGraph struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	edges []*Edge

	// Metadata
	RootPath string            `json:"root_path"`
	Topology TopologyType      `json:"topology"`
	Meta     map[string]string `json:"meta,omitempty"`
}

// TopologyType describes the overall project structure.
type TopologyType string

const (
	TopologyMonolith     TopologyType = "monolith"
	TopologyMonorepo     TopologyType = "monorepo"
	TopologyMicroservice TopologyType = "microservice"
	TopologyUnknown      TopologyType = "unknown"
)

// NewGraph creates an empty architecture graph.
func NewGraph(rootPath string) *ArchGraph {
	return &ArchGraph{
		nodes:    make(map[string]*Node),
		RootPath: rootPath,
		Topology: TopologyUnknown,
		Meta:     make(map[string]string),
	}
}

// AddNode adds a node to the graph. Returns false if a node with the same ID exists.
func (g *ArchGraph) AddNode(n *Node) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.nodes[n.ID]; exists {
		return false
	}
	g.nodes[n.ID] = n
	return true
}

// GetNode returns a node by ID, or nil if not found.
func (g *ArchGraph) GetNode(id string) *Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.nodes[id]
}

// Nodes returns all nodes sorted by ID for deterministic output.
func (g *ArchGraph) Nodes() []*Node {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make([]*Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		result = append(result, n)
	}
	return sortedByID(result)
}

// NodesByType returns nodes filtered by type.
func (g *ArchGraph) NodesByType(t NodeType) []*Node {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var result []*Node
	for _, n := range g.nodes {
		if n.Type == t {
			result = append(result, n)
		}
	}
	return sortedByID(result)
}

// sortedByID sorts nodes by ID in place and returns the slice for chaining.
func sortedByID(nodes []*Node) []*Node {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})
	return nodes
}

// AddEdge adds an edge to the graph. Returns false if an identical edge
// (same source, target, and type) already exists.
func (g *ArchGraph) AddEdge(e *Edge) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, existing := range g.edges {
		if edgesMatch(existing, e) {
			return false
		}
	}
	g.edges = append(g.edges, e)
	return true
}

// edgesMatch reports whether two edges share the same source, target, and type.
func edgesMatch(a, b *Edge) bool {
	return a.Source == b.Source && a.Target == b.Target && a.Type == b.Type
}

// Edges returns all edges.
func (g *ArchGraph) Edges() []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make([]*Edge, len(g.edges))
	copy(result, g.edges)
	return result
}

// EdgesFrom returns edges originating from a specific node.
func (g *ArchGraph) EdgesFrom(nodeID string) []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return filterEdges(g.edges, func(e *Edge) bool { return e.Source == nodeID })
}

// EdgesTo returns edges targeting a specific node.
func (g *ArchGraph) EdgesTo(nodeID string) []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return filterEdges(g.edges, func(e *Edge) bool { return e.Target == nodeID })
}

func filterEdges(edges []*Edge, keep func(*Edge) bool) []*Edge {
	var result []*Edge
	for _, e := range edges {
		if keep(e) {
			result = append(result, e)
		}
	}
	return result
}

// NodeCount returns the number of nodes.
func (g *ArchGraph) NodeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes)
}

// EdgeCount returns the number of edges.
func (g *ArchGraph) EdgeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.edges)
}

// Merge incorporates nodes and edges from another graph.
// Existing nodes with the same ID are skipped (first write wins).
func (g *ArchGraph) Merge(other *ArchGraph) {
	other.mu.RLock()
	defer other.mu.RUnlock()

	for _, n := range other.nodes {
		g.AddNode(n)
	}

	g.mu.Lock()
	g.edges = append(g.edges, other.edges...)
	g.mu.Unlock()
}

// HasCycle checks for circular dependencies using DFS.
func (g *ArchGraph) HasCycle() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	v := newCycleVisitor(g.edges)
	for id := range g.nodes {
		if v.visited[id] {
			continue
		}
		if v.dfs(id) {
			return true
		}
	}
	return false
}

// cycleVisitor encapsulates DFS state for dependency cycle detection.
type cycleVisitor struct {
	adj     map[string][]string
	visited map[string]bool
	inStack map[string]bool
}

func newCycleVisitor(edges []*Edge) *cycleVisitor {
	adj := make(map[string][]string)
	for _, e := range edges {
		if e.Type == EdgeDependency {
			adj[e.Source] = append(adj[e.Source], e.Target)
		}
	}
	return &cycleVisitor{
		adj:     adj,
		visited: make(map[string]bool),
		inStack: make(map[string]bool),
	}
}

func (v *cycleVisitor) dfs(node string) bool {
	v.visited[node] = true
	v.inStack[node] = true
	defer func() { v.inStack[node] = false }()

	for _, neighbor := range v.adj[node] {
		if !v.visited[neighbor] {
			if v.dfs(neighbor) {
				return true
			}
			continue
		}
		if v.inStack[neighbor] {
			return true
		}
	}
	return false
}

// RelativePaths converts absolute node paths to paths relative to the graph root.
func (g *ArchGraph) RelativePaths() {
	g.mu.Lock()
	defer g.mu.Unlock()

	prefix := g.RootPath + "/"
	for _, n := range g.nodes {
		if rel, ok := strings.CutPrefix(n.Path, prefix); ok {
			n.Path = rel
		}
	}
}

// ResolvedEdges returns edges with "import:" and "wikilink:" targets resolved
// to real node IDs. Edges whose target cannot be resolved are dropped.
// Self-loops and duplicates (by source|target|type) are removed.
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

// Summary returns a human-readable summary of the graph.
func (g *ArchGraph) Summary() string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	typeCounts := make(map[NodeType]int)
	for _, n := range g.nodes {
		typeCounts[n.Type]++
	}

	edgeTypeCounts := make(map[EdgeType]int)
	for _, e := range g.edges {
		edgeTypeCounts[e.Type]++
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Architecture Graph: %d nodes, %d edges\n", len(g.nodes), len(g.edges))
	fmt.Fprintf(&sb, "Topology: %s\n", g.Topology)
	fmt.Fprintf(&sb, "Root: %s\n", filepath.Base(g.RootPath))

	writeCountSection(&sb, "Nodes", typeCounts)
	writeCountSection(&sb, "Edges", edgeTypeCounts)
	return sb.String()
}

// writeCountSection appends a sorted "label: N type, M type, ..." line if counts is non-empty.
func writeCountSection[K ~string](sb *strings.Builder, label string, counts map[K]int) {
	if len(counts) == 0 {
		return
	}
	parts := make([]string, 0, len(counts))
	for t, c := range counts {
		parts = append(parts, fmt.Sprintf("%d %s", c, string(t)))
	}
	sort.Strings(parts)
	sb.WriteString(label)
	sb.WriteString(": ")
	sb.WriteString(strings.Join(parts, ", "))
	sb.WriteString("\n")
}
