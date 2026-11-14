package render

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// A file-level graph of a few hundred modules renders as a wall of boxes.
// GroupByFolder gives the readable version: one box per folder, and one arrow
// per pair of folders that says how many links it stands for.

// folderIDPrefix starts the ID of every folder node.
const folderIDPrefix = "dir:"

func isFolderID(id string) bool { return strings.HasPrefix(id, folderIDPrefix) }

// rootFolderName labels the group for files directly in the scan root.
const rootFolderName = "(root)"

// GroupByFolder returns a copy of graph in which every file-backed node
// (module, package, endpoint, note) is replaced by a node for its folder, cut
// to the first depth path segments below the scan root. Nodes that are not
// files (databases, queues, external APIs, services) are kept as they are.
//
// Edges are resolved first, then merged per source, target and type. The
// label carries the number of merged links ("12 imports"); links inside one
// folder are dropped. A depth below 1 returns graph unchanged.
func GroupByFolder(graph *model.ArchGraph, depth int) *model.ArchGraph {
	if depth < 1 {
		return graph
	}
	g := folderGrouper{root: graph.RootPath, depth: depth, groupOf: make(map[string]string)}
	out := model.NewGraph(graph.RootPath)
	out.Topology = graph.Topology
	out.Meta = graph.Meta

	members := make(map[string]int)
	for _, n := range graph.Nodes() {
		folder, ok := g.folder(n)
		if !ok {
			out.AddNode(n)
			continue
		}
		id := folderIDPrefix + folder
		g.groupOf[n.ID] = id
		members[id]++
	}
	for id, count := range members {
		folder := strings.TrimPrefix(id, folderIDPrefix)
		out.AddNode(&model.Node{
			ID:   id,
			Name: fmt.Sprintf("%s (%d)", folder, count),
			Type: model.NodeModule,
			Path: folder,
			Properties: map[string]string{
				"grouped": "folder",
				"members": fmt.Sprintf("%d", count),
			},
		})
	}
	for _, e := range g.mergedEdges(graph.ResolvedEdges()) {
		out.AddEdge(e)
	}
	return out
}

type folderGrouper struct {
	root    string
	depth   int
	groupOf map[string]string // node ID -> folder node ID
}

// folder returns the folder a node is grouped into, cut to the grouping
// depth. ok is false for nodes that do not stand for a file.
func (g folderGrouper) folder(n *model.Node) (string, bool) {
	if !fileBacked[n.Type] || n.Path == "" {
		return "", false
	}
	root := filepath.ToSlash(g.root)
	dir := strings.Trim(strings.TrimPrefix(nodeDir(n), root), "/")
	if dir == "" || dir == "." {
		return rootFolderName, true
	}
	segments := strings.Split(dir, "/")
	if len(segments) > g.depth {
		segments = segments[:g.depth]
	}
	return strings.Join(segments, "/"), true
}

// fileBacked lists the node types that stand for a file or a folder of files.
var fileBacked = map[model.NodeType]bool{
	model.NodeModule: true, model.NodePackage: true,
	model.NodeEndpoint: true, model.NodeNote: true,
}

// nodeDir returns the folder a file-backed node lives in. Module and package
// nodes carry their folder as Path; endpoints, notes and class-level nodes
// carry the file they were found in.
func nodeDir(n *model.Node) string {
	if pathIsFile(n) {
		return filepath.ToSlash(filepath.Dir(n.Path))
	}
	return filepath.ToSlash(n.Path)
}

func pathIsFile(n *model.Node) bool {
	return n.Type == model.NodeEndpoint || n.Type == model.NodeNote || filepath.Ext(n.Path) != ""
}

// groupedEdgeKey identifies the arrow a set of merged edges becomes.
type groupedEdgeKey struct {
	source, target string
	edgeType       model.EdgeType
}

func (k groupedEdgeKey) less(o groupedEdgeKey) bool {
	if k.source != o.source {
		return k.source < o.source
	}
	if k.target != o.target {
		return k.target < o.target
	}
	return k.edgeType < o.edgeType
}

// touchesFolder reports whether either end of the arrow is a folder node.
func (k groupedEdgeKey) touchesFolder() bool {
	return isFolderID(k.source) || isFolderID(k.target)
}

// mergedEdges maps each edge onto the grouped nodes and merges edges that end
// up with the same source, target and type. Output order is deterministic.
func (g folderGrouper) mergedEdges(edges []*model.Edge) []*model.Edge {
	counts := make(map[groupedEdgeKey]int)
	labels := make(map[groupedEdgeKey]string)
	for _, e := range edges {
		k := groupedEdgeKey{g.grouped(e.Source), g.grouped(e.Target), e.Type}
		if k.source == k.target {
			continue
		}
		counts[k]++
		labels[k] = e.Label
	}
	keys := make([]groupedEdgeKey, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })

	out := make([]*model.Edge, 0, len(keys))
	for _, k := range keys {
		out = append(out, &model.Edge{
			Source:     k.source,
			Target:     k.target,
			Type:       k.edgeType,
			Label:      mergedLabel(k, counts[k], labels[k]),
			Confidence: 1,
			Properties: map[string]string{"count": fmt.Sprintf("%d", counts[k])},
		})
	}
	return out
}

// mergedLabel words an arrow. One that touches a folder always gets a count,
// so "1 import" and "12 imports" read alike. Only a single edge between two
// ungrouped nodes keeps its own label.
func mergedLabel(k groupedEdgeKey, count int, original string) string {
	if count > 1 || k.touchesFolder() {
		return countLabel(count, k.edgeType)
	}
	return original
}

func (g folderGrouper) grouped(id string) string {
	if group, ok := g.groupOf[id]; ok {
		return group
	}
	return id
}

// countLabel words the number of merged links for an arrow.
func countLabel(count int, edgeType model.EdgeType) string {
	noun := strings.ReplaceAll(string(edgeType), "_", " ")
	switch edgeType {
	case model.EdgeDependency:
		noun = "import"
	case model.EdgeAPICall:
		noun = "call"
	}
	if count != 1 {
		noun += "s"
	}
	return fmt.Sprintf("%d %s", count, noun)
}
