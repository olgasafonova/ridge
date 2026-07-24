package detector

import (
	"sort"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// GraphSignals is graph-derived evidence behind a topology verdict: how the
// scanned dependency graph relates to the detected boundaries. Complements
// TopologySignals (filesystem markers) with the two signals only a scan can
// provide — coupling across boundaries and data-store sharing — so a consumer
// can check the verdict against the code, not just against marker files.
type GraphSignals struct {
	// CrossBoundaryEdges counts resolved dependency edges whose source and
	// target nodes live in different detected boundaries. High counts on a
	// "microservice"/"monorepo" verdict indicate the boundaries are not real
	// seams.
	CrossBoundaryEdges int `json:"cross_boundary_edges"`
	// SharedDatabase is true when at least one database node is reached from
	// two or more distinct boundaries — the classic distributed-monolith tell.
	SharedDatabase bool `json:"shared_database"`
	// SharedDatabaseIDs names the database nodes behind SharedDatabase so the
	// claim is checkable, not just asserted.
	SharedDatabaseIDs []string `json:"shared_database_ids,omitempty"`
}

// ComputeGraphSignals joins a scanned graph against detected boundaries and
// returns the graph-derived topology evidence. Returns nil when either input
// is missing or the boundary list is empty (no seams to measure against).
func ComputeGraphSignals(boundaries []Boundary, graph *model.ArchGraph) *GraphSignals {
	if graph == nil || len(boundaries) == 0 {
		return nil
	}

	nodeBoundary := assignNodesToBoundaries(boundaries, graph)
	gs := &GraphSignals{}
	dbSources := make(map[string]map[string]bool) // db node ID -> set of source boundaries

	for _, e := range graph.ResolvedEdges() {
		if crossesBoundary(nodeBoundary, e) {
			gs.CrossBoundaryEdges++
		}
		recordDatabaseTarget(graph, nodeBoundary, dbSources, e)
	}

	gs.SharedDatabaseIDs = sharedDatabaseIDs(dbSources)
	gs.SharedDatabase = len(gs.SharedDatabaseIDs) > 0
	return gs
}

// crossesBoundary reports whether the edge's source and target nodes are both
// boundary-assigned and live in different boundaries.
func crossesBoundary(nodeBoundary map[string]string, e *model.Edge) bool {
	sb, sok := nodeBoundary[e.Source]
	tb, tok := nodeBoundary[e.Target]
	return sok && tok && sb != tb
}

// recordDatabaseTarget records the source boundary of an edge whose target is
// a database node, building the db node ID -> source-boundary set used for
// shared-database detection.
func recordDatabaseTarget(graph *model.ArchGraph, nodeBoundary map[string]string, dbSources map[string]map[string]bool, e *model.Edge) {
	sb, ok := nodeBoundary[e.Source]
	if !ok {
		return
	}
	target := graph.GetNode(e.Target)
	if target == nil || target.Type != model.NodeDatabase {
		return
	}
	if dbSources[e.Target] == nil {
		dbSources[e.Target] = make(map[string]bool)
	}
	dbSources[e.Target][sb] = true
}

// sharedDatabaseIDs returns the sorted IDs of database nodes reached from two
// or more distinct boundaries, or nil when none qualify.
func sharedDatabaseIDs(dbSources map[string]map[string]bool) []string {
	var ids []string
	for dbID, sources := range dbSources {
		if len(sources) >= 2 {
			ids = append(ids, dbID)
		}
	}
	sort.Strings(ids)
	return ids
}

// assignNodesToBoundaries maps each file-backed node ID to the boundary whose
// path is the longest prefix of the node's project-relative path. The root
// boundary "." matches everything and acts as the fallback.
func assignNodesToBoundaries(boundaries []Boundary, graph *model.ArchGraph) map[string]string {
	out := make(map[string]string)
	for _, n := range graph.Nodes() {
		if n.Path == "" {
			continue
		}
		rel := relativeToRoot(n.Path, graph.RootPath)
		if b, ok := matchBoundary(boundaries, rel); ok {
			out[n.ID] = b
		}
	}
	return out
}

// relativeToRoot strips the graph root prefix from an absolute node path.
// Paths already relative (a prior RelativePaths call) pass through unchanged.
func relativeToRoot(nodePath, rootPath string) string {
	if rel, ok := strings.CutPrefix(nodePath, rootPath+"/"); ok {
		return rel
	}
	return nodePath
}

// matchBoundary returns the path of the boundary that most specifically
// contains rel: the longest boundary path that is a prefix directory of rel.
// The root boundary "." matches any path with specificity zero.
func matchBoundary(boundaries []Boundary, rel string) (string, bool) {
	best := ""
	bestLen := -1
	for _, b := range boundaries {
		switch {
		case b.Path == ".":
			if bestLen < 0 {
				best, bestLen = b.Path, 0
			}
		case rel == b.Path || strings.HasPrefix(rel, b.Path+"/"):
			if len(b.Path) > bestLen {
				best, bestLen = b.Path, len(b.Path)
			}
		}
	}
	return best, bestLen >= 0
}
