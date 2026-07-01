package detector

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectBoundaries_Monolith(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}

	if result.Topology != model.TopologyMonolith {
		t.Fatalf("expected monolith, got %s", result.Topology)
	}
}

// TestDetectBoundaries_SignalsAndReason is the design-for-verifiability check for
// the topology verdict: a confident call exposes the signals behind it, a reason,
// and is not flagged ambiguous.
func TestDetectBoundaries_SignalsAndReason(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}
	if result.Signals.GoModules != 1 {
		t.Errorf("signals.GoModules: want 1, got %d", result.Signals.GoModules)
	}
	if result.Reason == "" {
		t.Error("expected a non-empty reason behind the verdict")
	}
	if result.Ambiguous {
		t.Errorf("single-manifest monolith should not be ambiguous; reason=%q", result.Reason)
	}
}

// TestDetectBoundaries_CatchAllIsAmbiguous: several manifests, no stronger signal
// → the verdict comes from the weak catch-all branch and must be flagged ambiguous.
func TestDetectBoundaries_CatchAllIsAmbiguous(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/Cargo.toml", "[package]\nname='a'\n")
	writeFile(t, dir, "b/Cargo.toml", "[package]\nname='b'\n")
	writeFile(t, dir, "c/Cargo.toml", "[package]\nname='c'\n")

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}
	if result.Signals.TotalProjectMarkers != 3 {
		t.Errorf("TotalProjectMarkers: want 3, got %d", result.Signals.TotalProjectMarkers)
	}
	if !result.Ambiguous {
		t.Errorf("catch-all verdict should be ambiguous; topology=%s reason=%q", result.Topology, result.Reason)
	}
}

// TestDetectBoundaries_UnknownIsAmbiguous: no recognizable markers → unknown, ambiguous.
func TestDetectBoundaries_UnknownIsAmbiguous(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# hi\n")

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}
	if result.Topology != model.TopologyUnknown {
		t.Fatalf("expected unknown, got %s", result.Topology)
	}
	if !result.Ambiguous {
		t.Error("unknown topology must be flagged ambiguous")
	}
}

func TestDetectBoundaries_Monorepo_GoWork(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.work", "go 1.22\nuse ./svc-a\nuse ./svc-b\n")
	writeFile(t, dir, "svc-a/go.mod", "module example.com/svc-a\n")
	writeFile(t, dir, "svc-b/go.mod", "module example.com/svc-b\n")

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}

	if result.Topology != model.TopologyMonorepo {
		t.Fatalf("expected monorepo, got %s", result.Topology)
	}
}

func TestDetectBoundaries_Monorepo_MultipleGoMods(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "svc-a/go.mod", "module example.com/svc-a\n")
	writeFile(t, dir, "svc-b/go.mod", "module example.com/svc-b\n")

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}

	if result.Topology != model.TopologyMonorepo {
		t.Fatalf("expected monorepo, got %s", result.Topology)
	}
	if len(result.Boundaries) < 2 {
		t.Fatalf("expected at least 2 boundaries, got %d", len(result.Boundaries))
	}
}

func TestDetectBoundaries_Microservice(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\n")
	writeFile(t, dir, "svc-a/Dockerfile", "FROM golang:1.22\n")
	writeFile(t, dir, "svc-b/Dockerfile", "FROM golang:1.22\n")
	writeFile(t, dir, "docker-compose.yml", "version: '3'\n")

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}

	if result.Topology != model.TopologyMicroservice {
		t.Fatalf("expected microservice, got %s", result.Topology)
	}
}

func TestDetectBoundaries_CmdDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\n")
	writeFile(t, dir, "cmd/api/main.go", "package main\n")
	writeFile(t, dir, "cmd/worker/main.go", "package main\n")

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}

	// Multiple cmd/ dirs should be detected as boundaries
	cmdBoundaries := 0
	for _, b := range result.Boundaries {
		if b.Type == "service" {
			cmdBoundaries++
		}
	}
	if cmdBoundaries < 2 {
		t.Fatalf("expected at least 2 service boundaries from cmd/, got %d", cmdBoundaries)
	}
}

func TestDetectBoundaries_SkipsGitAndNodeModules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\n")
	writeFile(t, dir, ".git/config", "[core]\n")
	writeFile(t, dir, "node_modules/pkg/go.mod", "module fake\n")

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}

	// Should only find the root go.mod
	for _, b := range result.Boundaries {
		if b.Path == "node_modules/pkg" {
			t.Fatal("should not detect boundaries in node_modules")
		}
	}
}

func TestDetectBoundaries_NxMonorepo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"root"}`)
	writeFile(t, dir, "nx.json", `{}`)
	writeFile(t, dir, "apps/web/package.json", `{"name":"web"}`)

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}

	if result.Topology != model.TopologyMonorepo {
		t.Fatalf("expected monorepo for Nx project, got %s", result.Topology)
	}
}

func TestDetectBoundaries_TurborepoMonorepo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"root"}`)
	writeFile(t, dir, "turbo.json", `{"pipeline":{}}`)
	writeFile(t, dir, "apps/web/package.json", `{"name":"web"}`)

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}

	if result.Topology != model.TopologyMonorepo {
		t.Fatalf("expected monorepo for Turborepo project, got %s", result.Topology)
	}
}

func TestDetectBoundaries_RushMonorepo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"root"}`)
	writeFile(t, dir, "rush.json", `{"rushVersion":"5.0.0"}`)
	writeFile(t, dir, "apps/web/package.json", `{"name":"web"}`)

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}

	if result.Topology != model.TopologyMonorepo {
		t.Fatalf("expected monorepo for Rush project, got %s", result.Topology)
	}
}

func TestDetectBoundaries_MicroserviceWithoutOrchestration(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\n")
	writeFile(t, dir, "svc-a/Dockerfile", "FROM golang:1.22\n")
	writeFile(t, dir, "svc-b/Dockerfile", "FROM golang:1.22\n")
	writeFile(t, dir, "svc-c/Dockerfile", "FROM golang:1.22\n")

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}

	if result.Topology != model.TopologyMicroservice {
		t.Fatalf("expected microservice for 3+ Dockerfiles without orchestration, got %s", result.Topology)
	}
}

func TestDetectBoundaries_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	result, err := DetectBoundaries(dir)
	if err != nil {
		t.Fatalf("DetectBoundaries failed: %v", err)
	}

	if result.Topology != model.TopologyUnknown {
		t.Fatalf("expected unknown for empty dir, got %s", result.Topology)
	}
}
