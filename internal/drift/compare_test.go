package drift

import (
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func TestCompare_NoChanges(t *testing.T) {
	base := model.NewGraph("/tmp")
	base.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})
	base.AddEdge(&model.Edge{Source: "svc:api", Target: "db:pg", Type: model.EdgeReadWrite})

	current := model.NewGraph("/tmp")
	current.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})
	current.AddEdge(&model.Edge{Source: "svc:api", Target: "db:pg", Type: model.EdgeReadWrite})

	report := Compare(base, current)
	if report.HasChanges() {
		t.Fatalf("expected no changes, got %d", len(report.Changes))
	}
	if report.MaxSeverity != model.SeverityNone {
		t.Fatalf("expected none severity, got %s", report.MaxSeverity)
	}
}

func TestCompare_AddedService(t *testing.T) {
	base := model.NewGraph("/tmp")
	base.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})

	current := model.NewGraph("/tmp")
	current.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})
	current.AddNode(&model.Node{ID: "svc:worker", Name: "Worker", Type: model.NodeService})

	report := Compare(base, current)
	if !report.HasChanges() {
		t.Fatal("expected changes")
	}

	added := report.ChangesByType(model.ChangeAdded)
	if len(added) != 1 {
		t.Fatalf("expected 1 added, got %d", len(added))
	}
	if added[0].Subject != "svc:worker" {
		t.Fatalf("expected svc:worker added, got %s", added[0].Subject)
	}
	if report.MaxSeverity != model.SeverityHigh {
		t.Fatalf("adding a service should be high severity, got %s", report.MaxSeverity)
	}
}

func TestCompare_RemovedDatabase(t *testing.T) {
	base := model.NewGraph("/tmp")
	base.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})
	base.AddNode(&model.Node{ID: "db:pg", Name: "PostgreSQL", Type: model.NodeDatabase})

	current := model.NewGraph("/tmp")
	current.AddNode(&model.Node{ID: "svc:api", Name: "API", Type: model.NodeService})

	report := Compare(base, current)
	removed := report.ChangesByType(model.ChangeRemoved)
	if len(removed) != 1 {
		t.Fatalf("expected 1 removed, got %d", len(removed))
	}
	if report.MaxSeverity != model.SeverityHigh {
		t.Fatalf("removing a database should be high severity, got %s", report.MaxSeverity)
	}
}

func TestCompare_AddedEdge(t *testing.T) {
	base := model.NewGraph("/tmp")
	base.AddNode(&model.Node{ID: "a", Name: "A", Type: model.NodeService})
	base.AddNode(&model.Node{ID: "b", Name: "B", Type: model.NodeService})

	current := model.NewGraph("/tmp")
	current.AddNode(&model.Node{ID: "a", Name: "A", Type: model.NodeService})
	current.AddNode(&model.Node{ID: "b", Name: "B", Type: model.NodeService})
	current.AddEdge(&model.Edge{Source: "a", Target: "b", Type: model.EdgeDependency})

	report := Compare(base, current)
	added := report.ChangesByType(model.ChangeAdded)
	if len(added) != 1 {
		t.Fatalf("expected 1 added edge, got %d", len(added))
	}
	if added[0].Category != "edge" {
		t.Fatalf("expected edge category, got %s", added[0].Category)
	}
}

func TestCompare_CircularDependency(t *testing.T) {
	base := model.NewGraph("/tmp")
	base.AddNode(&model.Node{ID: "a", Name: "A", Type: model.NodePackage})
	base.AddNode(&model.Node{ID: "b", Name: "B", Type: model.NodePackage})
	base.AddEdge(&model.Edge{Source: "a", Target: "b", Type: model.EdgeDependency})

	current := model.NewGraph("/tmp")
	current.AddNode(&model.Node{ID: "a", Name: "A", Type: model.NodePackage})
	current.AddNode(&model.Node{ID: "b", Name: "B", Type: model.NodePackage})
	current.AddEdge(&model.Edge{Source: "a", Target: "b", Type: model.EdgeDependency})
	current.AddEdge(&model.Edge{Source: "b", Target: "a", Type: model.EdgeDependency})

	report := Compare(base, current)
	if report.MaxSeverity != model.SeverityCritical {
		t.Fatalf("circular dependency should be critical, got %s", report.MaxSeverity)
	}
}

func TestCompare_CircularDependency_ImportMediated(t *testing.T) {
	// Go/TS imports target unresolved "import:<path>" strings on raw edges.
	// The drift cycle check must resolve edges before DFS, otherwise this
	// mutual import cycle is a false negative in cycle_introduced drift.
	base := model.NewGraph("/tmp/project")
	base.AddNode(&model.Node{ID: "pkg:a", Name: "A", Type: model.NodePackage, Path: "/tmp/project/internal/a"})
	base.AddNode(&model.Node{ID: "pkg:b", Name: "B", Type: model.NodePackage, Path: "/tmp/project/internal/b"})
	base.AddEdge(&model.Edge{Source: "pkg:a", Target: "import:github.com/user/project/internal/b", Type: model.EdgeDependency, Confidence: 0.9})

	current := model.NewGraph("/tmp/project")
	current.AddNode(&model.Node{ID: "pkg:a", Name: "A", Type: model.NodePackage, Path: "/tmp/project/internal/a"})
	current.AddNode(&model.Node{ID: "pkg:b", Name: "B", Type: model.NodePackage, Path: "/tmp/project/internal/b"})
	current.AddEdge(&model.Edge{Source: "pkg:a", Target: "import:github.com/user/project/internal/b", Type: model.EdgeDependency, Confidence: 0.9})
	current.AddEdge(&model.Edge{Source: "pkg:b", Target: "import:github.com/user/project/internal/a", Type: model.EdgeDependency, Confidence: 0.9})

	report := Compare(base, current)
	if report.MaxSeverity != model.SeverityCritical {
		t.Fatalf("import-mediated circular dependency should be critical, got %s", report.MaxSeverity)
	}
}

func TestCompare_ModifiedNode(t *testing.T) {
	base := model.NewGraph("/tmp")
	base.AddNode(&model.Node{ID: "svc:api", Name: "API v1", Type: model.NodeService})

	current := model.NewGraph("/tmp")
	current.AddNode(&model.Node{ID: "svc:api", Name: "API v2", Type: model.NodeService})

	report := Compare(base, current)
	modified := report.ChangesByType(model.ChangeModified)
	if len(modified) != 1 {
		t.Fatalf("expected 1 modified, got %d", len(modified))
	}
}

func TestCompare_SummaryMessage(t *testing.T) {
	base := model.NewGraph("/tmp")

	current := model.NewGraph("/tmp")
	current.AddNode(&model.Node{ID: "svc:new", Name: "New", Type: model.NodeService})

	report := Compare(base, current)
	if report.Summary == "" {
		t.Fatal("expected non-empty summary")
	}
}
