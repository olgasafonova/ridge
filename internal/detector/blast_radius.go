package detector

import (
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// BlastRadiusEntry describes one node that transitively depends on a target.
type BlastRadiusEntry struct {
	NodeID   string   `json:"node_id"`
	NodeName string   `json:"node_name"`
	Depth    int      `json:"depth"`
	PathBack []string `json:"path_back"` // node IDs from this node back to the target
}

// BlastRadiusResult is the full output of ComputeBlastRadius.
type BlastRadiusResult struct {
	TargetID    string             `json:"target_id"`
	Direct      int                `json:"direct"` // count of depth-1 dependents
	Total       int                `json:"total"`
	MaxDepthHit bool               `json:"max_depth_hit"`
	Dependents  []BlastRadiusEntry `json:"dependents"`
}

// ResolveTargetToID maps a free-form target string to a node ID present in
// the graph. Tries an exact ID match first, then a path-suffix match against
// node IDs and node Path fields. Returns the resolved ID and true on success.
func ResolveTargetToID(g *model.ArchGraph, target string) (string, bool) {
	if target == "" {
		return "", false
	}
	if n := g.GetNode(target); n != nil {
		return n.ID, true
	}
	if best := bestSuffixMatch(g, target); best != "" {
		return best, true
	}
	return "", false
}

// bestSuffixMatch finds the node whose ID or Path ends with target. Prefers
// the shortest matching ID so a more specific node wins when several share a
// common suffix.
func bestSuffixMatch(g *model.ArchGraph, target string) string {
	var best string
	for _, n := range g.Nodes() {
		if !strings.HasSuffix(n.ID, target) && !strings.HasSuffix(n.Path, target) {
			continue
		}
		if best == "" || len(n.ID) < len(best) {
			best = n.ID
		}
	}
	return best
}

// reverseAdjacency builds a reverse adjacency map from resolved edges
// (target → list of source IDs).
func reverseAdjacency(g *model.ArchGraph) map[string][]string {
	reverse := make(map[string][]string)
	for _, e := range g.ResolvedEdges() {
		reverse[e.Target] = append(reverse[e.Target], e.Source)
	}
	return reverse
}

// blastRadiusState holds the mutable state of an in-progress BFS walk.
type blastRadiusState struct {
	g          *model.ArchGraph
	reverse    map[string][]string
	visited    map[string]bool
	parent     map[string]string
	targetID   string
	dependents []BlastRadiusEntry
	direct     int
}

// expand visits one dependent of curID at the given parent depth, recording
// it and returning the new queue entries.
type qItem struct {
	id    string
	depth int
}

func (s *blastRadiusState) expand(curID string, parentDepth int) []qItem {
	var enqueued []qItem
	for _, src := range s.reverse[curID] {
		if s.visited[src] {
			continue
		}
		s.visited[src] = true
		s.parent[src] = curID
		depth := parentDepth + 1
		if depth == 1 {
			s.direct++
		}
		s.dependents = append(s.dependents, BlastRadiusEntry{
			NodeID:   src,
			NodeName: nodeDisplayName(s.g, src),
			Depth:    depth,
			PathBack: tracePathBack(src, s.parent, s.targetID),
		})
		enqueued = append(enqueued, qItem{src, depth})
	}
	return enqueued
}

// nodeDisplayName returns the node's Name if non-empty, otherwise its ID.
func nodeDisplayName(g *model.ArchGraph, id string) string {
	if n := g.GetNode(id); n != nil && n.Name != "" {
		return n.Name
	}
	return id
}

// ComputeBlastRadius walks resolved edges in reverse from target, returning
// every node that transitively depends on it together with the shortest path
// back to the target. BFS so dependents are discovered in depth order.
//
// maxDepth caps the walk; pass 0 or negative to use the default of 50.
// Cycles are not double-traversed because each node is visited once.
func ComputeBlastRadius(g *model.ArchGraph, targetID string, maxDepth int) BlastRadiusResult {
	if maxDepth <= 0 {
		maxDepth = 50
	}
	state := &blastRadiusState{
		g:        g,
		reverse:  reverseAdjacency(g),
		visited:  map[string]bool{targetID: true},
		parent:   map[string]string{},
		targetID: targetID,
	}
	queue := []qItem{{targetID, 0}}
	maxHit := false

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			if len(state.reverse[cur.id]) > 0 {
				maxHit = true
			}
			continue
		}
		queue = append(queue, state.expand(cur.id, cur.depth)...)
	}

	return BlastRadiusResult{
		TargetID:    targetID,
		Direct:      state.direct,
		Total:       len(state.dependents),
		MaxDepthHit: maxHit,
		Dependents:  state.dependents,
	}
}

// tracePathBack walks the parent map from start to target, returning the
// chain of node IDs (start ... target). Returns just [start] if no chain
// is recorded; caller should not happen because BFS records every visited
// non-target node's parent before enqueueing it.
func tracePathBack(start string, parent map[string]string, target string) []string {
	chain := []string{start}
	cur := start
	for cur != target {
		next, ok := parent[cur]
		if !ok {
			break
		}
		chain = append(chain, next)
		cur = next
	}
	return chain
}
