package detector

import (
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

// twoBoundaryGraph builds a graph with one node in each of two boundaries and
// a database node, rooted at /repo.
func twoBoundaryGraph() *model.ArchGraph {
	g := model.NewGraph("/repo")
	g.AddNode(&model.Node{ID: "svc:api", Name: "api", Type: model.NodeService, Path: "/repo/services/api/main.go"})
	g.AddNode(&model.Node{ID: "svc:worker", Name: "worker", Type: model.NodeService, Path: "/repo/services/worker/main.go"})
	g.AddNode(&model.Node{ID: "db:pg", Name: "postgres", Type: model.NodeDatabase})
	return g
}

func twoServiceBoundaries() []Boundary {
	return []Boundary{
		{Name: "api", Path: "services/api", Type: "service", Markers: []string{"Dockerfile"}},
		{Name: "worker", Path: "services/worker", Type: "service", Markers: []string{"Dockerfile"}},
	}
}

func TestComputeGraphSignals_CrossBoundaryEdges(t *testing.T) {
	g := twoBoundaryGraph()
	g.AddEdge(&model.Edge{Source: "svc:api", Target: "svc:worker", Type: model.EdgeDependency})

	gs := ComputeGraphSignals(twoServiceBoundaries(), g)
	if gs == nil {
		t.Fatal("expected signals, got nil")
	}
	if gs.CrossBoundaryEdges != 1 {
		t.Errorf("CrossBoundaryEdges: want 1, got %d", gs.CrossBoundaryEdges)
	}
	if gs.SharedDatabase {
		t.Error("no database edges; SharedDatabase should be false")
	}
}

func TestComputeGraphSignals_SameBoundaryEdgeNotCounted(t *testing.T) {
	g := model.NewGraph("/repo")
	g.AddNode(&model.Node{ID: "a", Name: "a", Type: model.NodePackage, Path: "/repo/services/api/a.go"})
	g.AddNode(&model.Node{ID: "b", Name: "b", Type: model.NodePackage, Path: "/repo/services/api/sub/b.go"})
	g.AddEdge(&model.Edge{Source: "a", Target: "b", Type: model.EdgeDependency})

	gs := ComputeGraphSignals(twoServiceBoundaries(), g)
	if gs.CrossBoundaryEdges != 0 {
		t.Errorf("intra-boundary edge counted as cross-boundary: got %d", gs.CrossBoundaryEdges)
	}
}

func TestComputeGraphSignals_SharedDatabase(t *testing.T) {
	g := twoBoundaryGraph()
	g.AddEdge(&model.Edge{Source: "svc:api", Target: "db:pg", Type: model.EdgeReadWrite})
	g.AddEdge(&model.Edge{Source: "svc:worker", Target: "db:pg", Type: model.EdgeReadWrite})

	gs := ComputeGraphSignals(twoServiceBoundaries(), g)
	if !gs.SharedDatabase {
		t.Fatal("database reached from two boundaries; SharedDatabase should be true")
	}
	if len(gs.SharedDatabaseIDs) != 1 || gs.SharedDatabaseIDs[0] != "db:pg" {
		t.Errorf("SharedDatabaseIDs: want [db:pg], got %v", gs.SharedDatabaseIDs)
	}
}

func TestComputeGraphSignals_SingleBoundaryDatabaseNotShared(t *testing.T) {
	g := twoBoundaryGraph()
	g.AddEdge(&model.Edge{Source: "svc:api", Target: "db:pg", Type: model.EdgeReadWrite})

	gs := ComputeGraphSignals(twoServiceBoundaries(), g)
	if gs.SharedDatabase {
		t.Error("database reached from one boundary only; SharedDatabase should be false")
	}
	if len(gs.SharedDatabaseIDs) != 0 {
		t.Errorf("SharedDatabaseIDs should be empty, got %v", gs.SharedDatabaseIDs)
	}
}

func TestComputeGraphSignals_RootBoundaryFallback(t *testing.T) {
	// A "." boundary catches nodes outside any specific boundary; edges from
	// it into a specific boundary are cross-boundary.
	g := model.NewGraph("/repo")
	g.AddNode(&model.Node{ID: "shared", Name: "shared", Type: model.NodePackage, Path: "/repo/pkg/shared.go"})
	g.AddNode(&model.Node{ID: "api", Name: "api", Type: model.NodePackage, Path: "/repo/services/api/main.go"})
	g.AddEdge(&model.Edge{Source: "api", Target: "shared", Type: model.EdgeDependency})

	boundaries := []Boundary{
		{Name: "repo", Path: ".", Type: "module", Markers: []string{"go.mod"}},
		{Name: "api", Path: "services/api", Type: "service", Markers: []string{"Dockerfile"}},
	}
	gs := ComputeGraphSignals(boundaries, g)
	if gs.CrossBoundaryEdges != 1 {
		t.Errorf("CrossBoundaryEdges: want 1 (api -> root fallback), got %d", gs.CrossBoundaryEdges)
	}
}

func TestComputeGraphSignals_NilInputs(t *testing.T) {
	if gs := ComputeGraphSignals(twoServiceBoundaries(), nil); gs != nil {
		t.Errorf("nil graph: want nil signals, got %+v", gs)
	}
	if gs := ComputeGraphSignals(nil, twoBoundaryGraph()); gs != nil {
		t.Errorf("no boundaries: want nil signals, got %+v", gs)
	}
}

func TestComputeGraphSignals_RelativeNodePaths(t *testing.T) {
	// Node paths may already be root-relative if RelativePaths ran on the
	// cached graph; mapping must still work.
	g := model.NewGraph("/repo")
	g.AddNode(&model.Node{ID: "a", Name: "a", Type: model.NodePackage, Path: "services/api/a.go"})
	g.AddNode(&model.Node{ID: "w", Name: "w", Type: model.NodePackage, Path: "services/worker/w.go"})
	g.AddEdge(&model.Edge{Source: "a", Target: "w", Type: model.EdgeDependency})

	gs := ComputeGraphSignals(twoServiceBoundaries(), g)
	if gs.CrossBoundaryEdges != 1 {
		t.Errorf("CrossBoundaryEdges with relative paths: want 1, got %d", gs.CrossBoundaryEdges)
	}
}

func TestDetectBoundaries_DeployableUnits(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\ngo 1.22\n")
	writeFile(t, dir, "cmd/api/main.go", "package main\nfunc main() {}\n")
	writeFile(t, dir, "cmd/worker/main.go", "package main\nfunc main() {}\n")

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}
	if result.Signals.DeployableUnits != 2 {
		t.Errorf("DeployableUnits: want 2 (cmd/api, cmd/worker), got %d", result.Signals.DeployableUnits)
	}
}

func TestDetectBoundaries_DeployableUnits_RootDockerfile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\ngo 1.22\n")
	writeFile(t, dir, "Dockerfile", "FROM golang:1.22\n")

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}
	if result.Signals.DeployableUnits != 1 {
		t.Errorf("DeployableUnits: want 1 (root module with Dockerfile), got %d", result.Signals.DeployableUnits)
	}
}
