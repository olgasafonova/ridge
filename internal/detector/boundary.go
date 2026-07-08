// Package detector provides architecture detection: boundaries, topology, dataflow, and validation.
package detector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// BoundaryResult holds detected service/module boundaries.
type BoundaryResult struct {
	Topology   model.TopologyType
	Boundaries []Boundary
	Signals    TopologySignals // the counts/flags that drove the topology verdict
	Reason     string          // which rule fired, in plain language
	Ambiguous  bool            // true when the verdict is a fallback or a near-miss
}

// TopologySignals is the exported, falsifiable evidence behind the topology
// verdict: the marker counts and workspace/orchestration flags inferTopology
// reasons over. Surfacing it lets a consumer re-derive the verdict instead of
// trusting the label.
type TopologySignals struct {
	GoModules           int  `json:"go_modules"`
	PackageJSONs        int  `json:"package_jsons"`
	Dockerfiles         int  `json:"dockerfiles"`
	CmdDirs             int  `json:"cmd_dirs"`
	TotalProjectMarkers int  `json:"total_project_markers"`
	HasWorkspaceConfig  bool `json:"has_workspace_config"`
	HasDockerCompose    bool `json:"has_docker_compose"`
	HasK8s              bool `json:"has_k8s"`
	// DeployableUnits counts boundaries that ship independently: service-typed
	// boundaries (cmd/ subdirectories, standalone Dockerfiles) plus any module
	// boundary that also carries a Dockerfile. Derived from the detected
	// boundaries as supplementary evidence; it does not drive inferTopology.
	DeployableUnits int `json:"deployable_units"`
}

// Boundary represents a detected service or module boundary.
type Boundary struct {
	Name    string
	Path    string
	Type    string // "service", "module", "package"
	Markers []string
}

// boundaryMarkers holds the raw findings from one walk of the project tree.
type boundaryMarkers struct {
	goMods       []string
	packageJSONs []string
	dockerfiles  []string
	pyProjects   []string
	cargoTomls   []string
	pomXMLs      []string
	gradleBuilds []string
	cmdDirs      []string

	hasGoWork        bool
	hasNxJSON        bool
	hasTurboJSON     bool
	hasRushJSON      bool
	hasPnpmWorkspace bool
	hasDockerCompose bool
	hasK8sManifests  bool
}

var boundarySkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
}

// DetectBoundaries walks a directory tree and identifies service/module boundaries.
func DetectBoundaries(rootPath string) (*BoundaryResult, error) {
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}

	markers, err := collectBoundaryMarkers(absRoot)
	if err != nil {
		return nil, err
	}

	signal := markers.toSignal()
	topology, reason, ambiguous := inferTopology(signal)
	result := &BoundaryResult{
		Topology:  topology,
		Signals:   signal.export(),
		Reason:    reason,
		Ambiguous: ambiguous,
	}

	result.Boundaries = buildBoundaries(result.Boundaries, absRoot, markers)
	result.Signals.DeployableUnits = countDeployableUnits(result.Boundaries)
	return result, nil
}

// countDeployableUnits returns the number of boundaries that represent an
// independently-deployable unit: every service-typed boundary plus any module
// boundary augmented with a Dockerfile marker.
func countDeployableUnits(boundaries []Boundary) int {
	count := 0
	for _, b := range boundaries {
		if b.Type == "service" || hasMarker(b, "Dockerfile") {
			count++
		}
	}
	return count
}

// hasMarker reports whether the boundary carries the named marker file.
func hasMarker(b Boundary, marker string) bool {
	for _, m := range b.Markers {
		if m == marker {
			return true
		}
	}
	return false
}

// collectBoundaryMarkers walks the tree once and returns every project-level
// marker we recognize: per-language manifests, container files, workspace
// configs, k8s manifests, and cmd/* subdirectories.
func collectBoundaryMarkers(absRoot string) (*boundaryMarkers, error) {
	m := &boundaryMarkers{}
	err := filepath.WalkDir(absRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if boundarySkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			if d.Name() == "cmd" {
				m.cmdDirs = append(m.cmdDirs, listSubdirs(path)...)
			}
			return nil
		}
		rel, _ := filepath.Rel(absRoot, path)
		classifyMarkerFile(m, fileLoc{name: d.Name(), rel: rel})
		return nil
	})
	return m, err
}

// fileLoc is a marker file's basename plus its relative path inside the
// scanned project. Bundling these tames a string-heavy signature.
type fileLoc struct {
	name string
	rel  string
}

// classifyMarkerFile updates m based on a single file name + relative path.
func classifyMarkerFile(m *boundaryMarkers, loc fileLoc) {
	if classify, ok := boundaryClassifiers[loc.name]; ok {
		classify(m, loc.rel)
	}
	if isInDeployDir(loc) {
		m.hasK8sManifests = true
	}
}

// isInDeployDir returns true for *.yaml/*.yml files under k8s/, kubernetes/, or deploy/.
func isInDeployDir(loc fileLoc) bool {
	if !strings.HasSuffix(loc.name, ".yaml") && !strings.HasSuffix(loc.name, ".yml") {
		return false
	}
	dir := filepath.Dir(loc.rel)
	return strings.Contains(dir, "k8s") || strings.Contains(dir, "kubernetes") || strings.Contains(dir, "deploy")
}

// listSubdirs returns absolute paths of every direct subdirectory under path.
func listSubdirs(path string) []string {
	var out []string
	entries, _ := os.ReadDir(path)
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(path, e.Name()))
		}
	}
	return out
}

// buildBoundaries turns the collected markers into a deduped Boundary slice.
// Order matches the original implementation: Go modules, cmd dirs, Dockerfiles
// (which augment existing entries), then Python/Rust/Maven/Gradle modules.
func buildBoundaries(boundaries []Boundary, absRoot string, m *boundaryMarkers) []Boundary {
	for _, mod := range m.goMods {
		dir := filepath.Dir(mod)
		boundaries = append(boundaries, Boundary{
			Name:    boundaryName(dir, absRoot),
			Path:    dir,
			Type:    "module",
			Markers: []string{"go.mod"},
		})
	}
	for _, cmd := range m.cmdDirs {
		rel, _ := filepath.Rel(absRoot, cmd)
		boundaries = append(boundaries, Boundary{
			Name:    filepath.Base(cmd),
			Path:    rel,
			Type:    "service",
			Markers: []string{"cmd/ directory"},
		})
	}
	for _, df := range m.dockerfiles {
		boundaries = appendOrAugment(boundaries, boundaryAt(df, absRoot, boundaryKind{typ: "service", marker: "Dockerfile"}), augmentExisting)
	}
	for _, py := range m.pyProjects {
		boundaries = appendOrAugment(boundaries, boundaryAt(py, absRoot, boundaryKind{typ: "module", marker: filepath.Base(py)}), skipExisting)
	}
	for _, cargo := range m.cargoTomls {
		boundaries = appendOrAugment(boundaries, boundaryAt(cargo, absRoot, boundaryKind{typ: "module", marker: "Cargo.toml"}), skipExisting)
	}
	for _, pom := range m.pomXMLs {
		boundaries = appendOrAugment(boundaries, boundaryAt(pom, absRoot, boundaryKind{typ: "module", marker: "pom.xml"}), skipExisting)
	}
	for _, gradle := range m.gradleBuilds {
		boundaries = appendOrAugment(boundaries, boundaryAt(gradle, absRoot, boundaryKind{typ: "module", marker: filepath.Base(gradle)}), skipExisting)
	}
	return boundaries
}

// dedupMode controls what appendOrAugment does when a boundary at the same
// path already exists.
type dedupMode int

const (
	skipExisting    dedupMode = iota // leave the existing boundary untouched
	augmentExisting                  // append our marker to the existing boundary
)

// boundaryKind labels what kind of boundary a marker produces.
type boundaryKind struct {
	typ    string // "service" or "module"
	marker string // file name to record on the Boundary
}

// boundaryAt builds a Boundary from a marker file's relative path.
func boundaryAt(markerPath, absRoot string, kind boundaryKind) Boundary {
	dir := filepath.Dir(markerPath)
	return Boundary{
		Name:    boundaryName(dir, absRoot),
		Path:    dir,
		Type:    kind.typ,
		Markers: []string{kind.marker},
	}
}

// appendOrAugment adds b to boundaries, or — if a boundary at b.Path already
// exists — augments it according to mode.
func appendOrAugment(boundaries []Boundary, b Boundary, mode dedupMode) []Boundary {
	for i := range boundaries {
		if boundaries[i].Path != b.Path {
			continue
		}
		if mode == augmentExisting {
			boundaries[i].Markers = append(boundaries[i].Markers, b.Markers...)
		}
		return boundaries
	}
	return append(boundaries, b)
}

// boundaryName returns the directory's basename, falling back to the project
// root's basename when dir is "." (the project root itself).
func boundaryName(dir, absRoot string) string {
	if dir == "." {
		return filepath.Base(absRoot)
	}
	return filepath.Base(dir)
}

// topologySignal aggregates the marker counts and flags that drive
// inferTopology. Methods encapsulate the individual rules; ordering of
// checks in inferTopology is significant and matches the original.
type topologySignal struct {
	goModCount          int
	pkgJSONCount        int
	dockerfileCount     int
	cmdCount            int
	totalProjectMarkers int

	hasGoWork        bool
	hasNx            bool
	hasTurbo         bool
	hasRush          bool
	hasPnpmWorkspace bool
	hasDockerCompose bool
	hasK8s           bool
}

func (m *boundaryMarkers) toSignal() topologySignal {
	return topologySignal{
		goModCount:      len(m.goMods),
		pkgJSONCount:    len(m.packageJSONs),
		dockerfileCount: len(m.dockerfiles),
		cmdCount:        len(m.cmdDirs),
		totalProjectMarkers: len(m.goMods) + len(m.packageJSONs) +
			len(m.pyProjects) + len(m.cargoTomls) +
			len(m.pomXMLs) + len(m.gradleBuilds),
		hasGoWork:        m.hasGoWork,
		hasNx:            m.hasNxJSON,
		hasTurbo:         m.hasTurboJSON,
		hasRush:          m.hasRushJSON,
		hasPnpmWorkspace: m.hasPnpmWorkspace,
		hasDockerCompose: m.hasDockerCompose,
		hasK8s:           m.hasK8sManifests,
	}
}

func (s topologySignal) hasWorkspaceConfig() bool {
	return s.hasGoWork || s.hasNx || s.hasTurbo || s.hasRush || s.hasPnpmWorkspace
}

func (s topologySignal) hasOrchestratedMicroservices() bool {
	return s.dockerfileCount > 1 && (s.hasDockerCompose || s.hasK8s)
}

func (s topologySignal) isSingleProjectMonolith() bool {
	return (s.goModCount == 1 || s.pkgJSONCount == 1) && s.dockerfileCount <= 1
}

// export projects the internal signal onto the public, JSON-serializable shape.
func (s topologySignal) export() TopologySignals {
	return TopologySignals{
		GoModules:           s.goModCount,
		PackageJSONs:        s.pkgJSONCount,
		Dockerfiles:         s.dockerfileCount,
		CmdDirs:             s.cmdCount,
		TotalProjectMarkers: s.totalProjectMarkers,
		HasWorkspaceConfig:  s.hasWorkspaceConfig(),
		HasDockerCompose:    s.hasDockerCompose,
		HasK8s:              s.hasK8s,
	}
}

// inferTopology returns the topology verdict, the plain-language reason for it,
// and whether the verdict is ambiguous. Ambiguity fires when the verdict came
// from a weak catch-all branch or when a competing topology was a near-miss, so
// a consumer can treat a borderline call with appropriate caution rather than as
// a confident classification. Branch order is significant and unchanged.
func inferTopology(s topologySignal) (topology model.TopologyType, reason string, ambiguous bool) {
	switch {
	case s.hasWorkspaceConfig():
		return model.TopologyMonorepo, "workspace config present (go.work / nx / turbo / rush / pnpm)", false
	case s.goModCount > 1:
		return model.TopologyMonorepo, fmt.Sprintf("%d go modules (>1)", s.goModCount), false
	case s.hasOrchestratedMicroservices():
		return model.TopologyMicroservice,
			fmt.Sprintf("%d Dockerfiles with orchestration (compose/k8s)", s.dockerfileCount), false
	case s.dockerfileCount > 2:
		return model.TopologyMicroservice, fmt.Sprintf("%d Dockerfiles (>2), no orchestration", s.dockerfileCount), false
	case s.cmdCount > 1:
		return model.TopologyMonorepo, fmt.Sprintf("%d cmd/ subdirectories (>1)", s.cmdCount), false
	case s.totalProjectMarkers > 2:
		// Catch-all: several manifests but none of the stronger rules fired.
		return model.TopologyMonorepo,
			fmt.Sprintf("catch-all: %d project markers (>2), no stronger signal", s.totalProjectMarkers), true
	case s.isSingleProjectMonolith():
		return model.TopologyMonolith, "single project manifest, <=1 Dockerfile", false
	}
	// Fallback: no rule matched. Includes the near-microservice case of exactly
	// 2 Dockerfiles without orchestration, which clears no stronger threshold.
	return model.TopologyUnknown, "no recognizable topology signal", true
}
