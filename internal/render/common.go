package render

import (
	"sort"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// FilterNodesByViewLevel filters nodes based on the requested view level.
func FilterNodesByViewLevel(nodes []*model.Node, level ViewLevel) []*model.Node {
	keep := keepByViewLevel(level)
	var result []*model.Node
	for _, n := range nodes {
		if keep(n) {
			result = append(result, n)
		}
	}
	return result
}

func keepByViewLevel(level ViewLevel) func(*model.Node) bool {
	switch level {
	case ViewSystem:
		return func(n *model.Node) bool {
			return n.Type == model.NodeService || n.Type == model.NodeExternalAPI
		}
	case ViewContainer:
		return func(n *model.Node) bool {
			return n.Type != model.NodePackage && n.Type != model.NodeEndpoint
		}
	default:
		return func(*model.Node) bool { return true }
	}
}

// VisibleGraph holds filtered nodes and edges for rendering.
type VisibleGraph struct {
	Nodes       []*model.Node
	Edges       []*model.Edge
	IDs         map[string]bool
	Names       map[string]string // node ID → display name
	PrunedNodes []string          // names of nodes removed by super-node pruning
}

// FilterGraph returns nodes and edges visible at the given view level.
// Edges are included only if both source and target are visible.
// Import edges (target "import:X") are resolved to internal package nodes
// via ArchGraph.ResolvedEdges().
func FilterGraph(graph *model.ArchGraph, level ViewLevel) *VisibleGraph {
	nodes := FilterNodesByViewLevel(graph.Nodes(), level)
	ids := make(map[string]bool, len(nodes))
	names := make(map[string]string, len(nodes))
	for _, n := range nodes {
		ids[n.ID] = true
		names[n.ID] = n.Name
	}

	var edges []*model.Edge
	for _, e := range graph.ResolvedEdges() {
		if !ids[e.Source] || !ids[e.Target] {
			continue
		}
		edges = append(edges, e)
	}

	return &VisibleGraph{Nodes: nodes, Edges: edges, IDs: ids, Names: names}
}

// TransitiveReduce removes edges that are implied by longer paths.
// For each edge (u→v) of type T, if v is reachable from u through
// intermediate nodes using only edges of type T, the direct edge is redundant.
func (vg *VisibleGraph) TransitiveReduce() {
	adj := buildEdgeTypeAdjacency(vg.Edges)
	vg.Edges = filterRenderEdges(vg.Edges, func(e *model.Edge) bool {
		return !transitiveReachable(adj[string(e.Type)], e.Source, e.Target)
	})
}

// buildEdgeTypeAdjacency groups outgoing edges by type → source → targets.
func buildEdgeTypeAdjacency(edges []*model.Edge) map[string]map[string][]string {
	adj := make(map[string]map[string][]string)
	for _, e := range edges {
		t := string(e.Type)
		if adj[t] == nil {
			adj[t] = make(map[string][]string)
		}
		adj[t][e.Source] = append(adj[t][e.Source], e.Target)
	}
	return adj
}

// transitiveReachable checks if target is reachable from source via a path
// of length >= 2 (through intermediate nodes).
func transitiveReachable(adj map[string][]string, source, target string) bool {
	visited := map[string]bool{source: true}
	queue := seedTransitiveQueue(adj[source], target, visited)

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if curr == target {
			return true
		}
		queue = enqueueUnvisited(queue, adj[curr], visited)
	}
	return false
}

// enqueueUnvisited appends not-yet-visited successors of curr onto queue.
func enqueueUnvisited(queue, successors []string, visited map[string]bool) []string {
	for _, next := range successors {
		if visited[next] {
			continue
		}
		visited[next] = true
		queue = append(queue, next)
	}
	return queue
}

// seedTransitiveQueue primes BFS with source's neighbors, excluding the direct
// target (which we're trying to prove reachable via length >= 2).
func seedTransitiveQueue(neighbors []string, target string, visited map[string]bool) []string {
	queue := make([]string, 0, len(neighbors))
	for _, neighbor := range neighbors {
		if neighbor == target || visited[neighbor] {
			continue
		}
		visited[neighbor] = true
		queue = append(queue, neighbor)
	}
	return queue
}

// ComputeNodeDepths returns each node's depth (longest outgoing path within
// the visible graph) and the overall maximum depth. Cycles tie-break at 0.
// Shared by drawio and excalidraw layout.
func ComputeNodeDepths(vg *VisibleGraph) (map[string]int, int) {
	w := &depthWalker{
		outgoing:  buildVisibleOutgoing(vg),
		depths:    make(map[string]int, len(vg.Nodes)),
		computing: make(map[string]bool),
	}
	maxDepth := 0
	for _, n := range vg.Nodes {
		if d := w.depth(n.ID); d > maxDepth {
			maxDepth = d
		}
	}
	return w.depths, maxDepth
}

// buildVisibleOutgoing returns source→targets for edges where both endpoints
// are visible nodes.
func buildVisibleOutgoing(vg *VisibleGraph) map[string][]string {
	nodeIDs := make(map[string]bool, len(vg.Nodes))
	for _, n := range vg.Nodes {
		nodeIDs[n.ID] = true
	}
	outgoing := make(map[string][]string)
	for _, e := range vg.Edges {
		if !nodeIDs[e.Source] || !nodeIDs[e.Target] {
			continue
		}
		outgoing[e.Source] = append(outgoing[e.Source], e.Target)
	}
	return outgoing
}

type depthWalker struct {
	outgoing  map[string][]string
	depths    map[string]int
	computing map[string]bool
}

func (w *depthWalker) depth(id string) int {
	if d, ok := w.depths[id]; ok {
		return d
	}
	if w.computing[id] {
		return 0
	}
	w.computing[id] = true
	maxChild := -1
	for _, t := range w.outgoing[id] {
		if cd := w.depth(t); cd > maxChild {
			maxChild = cd
		}
	}
	d := maxChild + 1
	w.depths[id] = d
	delete(w.computing, id)
	return d
}

// LayerByDepth buckets nodes into depth layers (index 0 = leaves).
func LayerByDepth(nodes []*model.Node, depths map[string]int, maxDepth int) [][]*model.Node {
	layers := make([][]*model.Node, maxDepth+1)
	for _, n := range nodes {
		layers[depths[n.ID]] = append(layers[depths[n.ID]], n)
	}
	return layers
}

// BarycenterOrder reorders nodes within each topological layer to minimize
// edge crossings. Two passes (top-down, then bottom-up) suffice for most DAGs.
func BarycenterOrder(layerNodes [][]*model.Node, edges []*model.Edge) {
	if len(layerNodes) < 2 {
		return
	}
	bo := &barycenterOrderer{
		layerNodes: layerNodes,
		neighbors:  buildNeighbors(edges),
		posOf:      make(map[string]int),
	}
	bo.rebuildPos()

	for i := 1; i < len(layerNodes); i++ {
		bo.sortLayer(layerNodes[i])
		bo.rebuildPos()
	}
	for i := len(layerNodes) - 2; i >= 0; i-- {
		bo.sortLayer(layerNodes[i])
		bo.rebuildPos()
	}
}

type barycenterOrderer struct {
	layerNodes [][]*model.Node
	neighbors  map[string][]string
	posOf      map[string]int
}

func (bo *barycenterOrderer) rebuildPos() {
	for _, layer := range bo.layerNodes {
		for j, n := range layer {
			bo.posOf[n.ID] = j
		}
	}
}

func (bo *barycenterOrderer) sortLayer(layer []*model.Node) {
	bary := make(map[string]float64, len(layer))
	for _, n := range layer {
		bary[n.ID] = bo.barycenter(n.ID)
	}
	sort.SliceStable(layer, func(i, j int) bool {
		return bary[layer[i].ID] < bary[layer[j].ID]
	})
}

func (bo *barycenterOrderer) barycenter(nodeID string) float64 {
	nbrs := bo.neighbors[nodeID]
	if len(nbrs) == 0 {
		return float64(bo.posOf[nodeID])
	}
	sum := 0.0
	for _, nb := range nbrs {
		sum += float64(bo.posOf[nb])
	}
	return sum / float64(len(nbrs))
}

func buildNeighbors(edges []*model.Edge) map[string][]string {
	neighbors := make(map[string][]string)
	for _, e := range edges {
		neighbors[e.Source] = append(neighbors[e.Source], e.Target)
		neighbors[e.Target] = append(neighbors[e.Target], e.Source)
	}
	return neighbors
}

// EdgeLabel returns a display label for an edge. It suppresses labels that
// match the edge type name (e.g., "dependency" on a dependency edge) or that
// duplicate the target node name, since both are redundant noise.
func EdgeLabel(e *model.Edge, targetName string) string {
	if isRedundantEdgeLabel(e.Label, e.Type, targetName) {
		return ""
	}
	return e.Label
}

func isRedundantEdgeLabel(label string, edgeType model.EdgeType, targetName string) bool {
	if label == "" {
		return true
	}
	if label == string(edgeType) {
		return true
	}
	return label == targetName
}

// PruneSuperNodes removes nodes whose fan-in ratio exceeds the threshold.
// Fan-in ratio = (unique sources targeting this node) / (total unique source nodes).
// Returns the names of pruned nodes.
func PruneSuperNodes(vg *VisibleGraph, threshold float64) []string {
	if threshold <= 0 || len(vg.Edges) == 0 {
		return nil
	}
	pruneSet := superNodeIDs(vg, threshold)
	if len(pruneSet) == 0 {
		return nil
	}
	return applyPrune(vg, pruneSet)
}

// superNodeIDs returns the set of node IDs whose fan-in ratio exceeds the threshold.
func superNodeIDs(vg *VisibleGraph, threshold float64) map[string]bool {
	totalSources := countUniqueSources(vg.Edges)
	if totalSources == 0 {
		return nil
	}
	fanIn := uniqueSourcesPerTarget(vg.Edges)

	pruneSet := make(map[string]bool)
	for nodeID, srcSet := range fanIn {
		if float64(len(srcSet))/float64(totalSources) > threshold {
			pruneSet[nodeID] = true
		}
	}
	return pruneSet
}

func countUniqueSources(edges []*model.Edge) int {
	sources := make(map[string]bool)
	for _, e := range edges {
		sources[e.Source] = true
	}
	return len(sources)
}

func uniqueSourcesPerTarget(edges []*model.Edge) map[string]map[string]bool {
	fanIn := make(map[string]map[string]bool)
	for _, e := range edges {
		if fanIn[e.Target] == nil {
			fanIn[e.Target] = make(map[string]bool)
		}
		fanIn[e.Target][e.Source] = true
	}
	return fanIn
}

// applyPrune removes nodes/edges in pruneSet from vg and returns the pruned names.
func applyPrune(vg *VisibleGraph, pruneSet map[string]bool) []string {
	nameOf := make(map[string]string, len(vg.Nodes))
	for _, n := range vg.Nodes {
		nameOf[n.ID] = n.Name
	}

	prunedNames := make([]string, 0, len(pruneSet))
	for id := range pruneSet {
		prunedNames = append(prunedNames, nameOf[id])
		delete(vg.IDs, id)
	}

	vg.Nodes = filterNodes(vg.Nodes, func(n *model.Node) bool { return !pruneSet[n.ID] })
	vg.Edges = filterRenderEdges(vg.Edges, func(e *model.Edge) bool {
		return !pruneSet[e.Source] && !pruneSet[e.Target]
	})
	vg.PrunedNodes = prunedNames
	return prunedNames
}

// KeepHighDegree drops nodes whose total degree (incoming + outgoing edges)
// is below minDegree. The complement of PruneSuperNodes: useful for
// knowledge-graph-shaped inputs where the meaningful structure is a small set
// of hubs and most files are leaves.
//
// Applied iteratively until stable: a node that loses all its edges because
// its only neighbors were leaves below the threshold is itself dropped.
func KeepHighDegree(vg *VisibleGraph, minDegree int) {
	if minDegree <= 0 || len(vg.Nodes) == 0 {
		return
	}
	for vg.dropBelowDegree(minDegree) {
		// keep iterating until no node falls below the threshold
	}
}

// dropBelowDegree performs one cull pass. Returns true if any node was dropped.
func (vg *VisibleGraph) dropBelowDegree(minDegree int) bool {
	degree := make(map[string]int, len(vg.Nodes))
	for _, e := range vg.Edges {
		degree[e.Source]++
		degree[e.Target]++
	}

	keep := make(map[string]bool, len(vg.Nodes))
	dropped := 0
	for _, n := range vg.Nodes {
		if degree[n.ID] >= minDegree {
			keep[n.ID] = true
		} else {
			dropped++
			delete(vg.IDs, n.ID)
		}
	}
	if dropped == 0 {
		return false
	}

	vg.Nodes = filterNodes(vg.Nodes, func(n *model.Node) bool { return keep[n.ID] })
	vg.Edges = filterRenderEdges(vg.Edges, func(e *model.Edge) bool {
		return keep[e.Source] && keep[e.Target]
	})
	return true
}

func filterNodes(nodes []*model.Node, keep func(*model.Node) bool) []*model.Node {
	filtered := nodes[:0]
	for _, n := range nodes {
		if keep(n) {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

func filterRenderEdges(edges []*model.Edge, keep func(*model.Edge) bool) []*model.Edge {
	filtered := edges[:0]
	for _, e := range edges {
		if keep(e) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// PrepareGraph applies view-level filtering and optional super-node pruning.
func PrepareGraph(graph *model.ArchGraph, opts Options) *VisibleGraph {
	vg := FilterGraph(graph, opts.ViewLevel)
	PruneSuperNodes(vg, opts.PruneThreshold)
	KeepHighDegree(vg, opts.MinDegree)
	return vg
}

// sanitizeIDReplacer pre-builds the structural-character replacements once.
// Different separators use distinct replacements to avoid collisions
// (e.g., "api/v1" and "api.v1" produce different IDs).
var sanitizeIDReplacer = strings.NewReplacer(
	"/", "__",
	":", "___",
	".", "_",
	" ", "_",
	"-", "_",
)

// SanitizeID replaces characters that are invalid in diagram node IDs.
// Any remaining non-alphanumeric character is replaced with a single underscore
// so IDs derived from filesystem paths (e.g. note basenames containing parens
// or punctuation) parse correctly across all diagram formats.
func SanitizeID(id string) string {
	return sanitizeAlnum(sanitizeIDReplacer.Replace(id))
}

// sanitizeAlnum replaces any non-alphanumeric/underscore rune with "_".
func sanitizeAlnum(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, ch := range s {
		switch {
		case ch >= 'a' && ch <= 'z',
			ch >= 'A' && ch <= 'Z',
			ch >= '0' && ch <= '9',
			ch == '_':
			sb.WriteRune(ch)
		default:
			sb.WriteByte('_')
		}
	}
	return sb.String()
}
