// Package detector provides architecture detection: boundaries, topology, dataflow, and validation.
package detector

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// BoundaryResult holds detected service/module boundaries.
type BoundaryResult struct {
	Topology   model.TopologyType
	Boundaries []Boundary
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

	result := &BoundaryResult{
		Topology: inferTopology(markers.toSignal()),
	}

	result.Boundaries = buildBoundaries(result.Boundaries, absRoot, markers)
	return result, nil
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
		classifyMarkerFile(m, d.Name(), rel)
		return nil
	})
	return m, err
}

// markerClassifier mutates a boundaryMarkers entry based on a relative path.
type markerClassifier func(m *boundaryMarkers, rel string)

// boundaryClassifiers dispatches a known marker file name to its classifier.
// Keeping the table data-driven keeps classifyMarkerFile cc-clean.
var boundaryClassifiers = map[string]markerClassifier{
	"go.mod":              func(m *boundaryMarkers, rel string) { m.goMods = append(m.goMods, rel) },
	"go.work":             func(m *boundaryMarkers, _ string) { m.hasGoWork = true },
	"package.json":        func(m *boundaryMarkers, rel string) { m.packageJSONs = append(m.packageJSONs, rel) },
	"nx.json":             func(m *boundaryMarkers, _ string) { m.hasNxJSON = true },
	"turbo.json":          func(m *boundaryMarkers, _ string) { m.hasTurboJSON = true },
	"rush.json":           func(m *boundaryMarkers, _ string) { m.hasRushJSON = true },
	"pnpm-workspace.yaml": func(m *boundaryMarkers, _ string) { m.hasPnpmWorkspace = true },
	"Dockerfile":          appendDockerfile,
	"dockerfile":          appendDockerfile,
	"docker-compose.yml":  setHasDockerCompose,
	"docker-compose.yaml": setHasDockerCompose,
	"compose.yml":         setHasDockerCompose,
	"compose.yaml":        setHasDockerCompose,
	"pyproject.toml":      appendPyProject,
	"setup.py":            appendPyProject,
	"setup.cfg":           appendPyProject,
	"Cargo.toml":          func(m *boundaryMarkers, rel string) { m.cargoTomls = append(m.cargoTomls, rel) },
	"pom.xml":             func(m *boundaryMarkers, rel string) { m.pomXMLs = append(m.pomXMLs, rel) },
	"build.gradle":        appendGradleBuild,
	"build.gradle.kts":    appendGradleBuild,
}

func appendDockerfile(m *boundaryMarkers, rel string)  { m.dockerfiles = append(m.dockerfiles, rel) }
func setHasDockerCompose(m *boundaryMarkers, _ string) { m.hasDockerCompose = true }
func appendPyProject(m *boundaryMarkers, rel string)   { m.pyProjects = append(m.pyProjects, rel) }
func appendGradleBuild(m *boundaryMarkers, rel string) { m.gradleBuilds = append(m.gradleBuilds, rel) }

// classifyMarkerFile updates m based on a single file name + relative path.
func classifyMarkerFile(m *boundaryMarkers, name, rel string) {
	if classify, ok := boundaryClassifiers[name]; ok {
		classify(m, rel)
	}
	if isInDeployDir(name, rel) {
		m.hasK8sManifests = true
	}
}

// isInDeployDir returns true for *.yaml/*.yml files under k8s/, kubernetes/, or deploy/.
func isInDeployDir(name, rel string) bool {
	if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
		return false
	}
	dir := filepath.Dir(rel)
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
		boundaries = appendOrAugment(boundaries, boundaryAt(df, absRoot, "service", "Dockerfile"), augmentExisting)
	}
	for _, py := range m.pyProjects {
		boundaries = appendOrAugment(boundaries, boundaryAt(py, absRoot, "module", filepath.Base(py)), skipExisting)
	}
	for _, cargo := range m.cargoTomls {
		boundaries = appendOrAugment(boundaries, boundaryAt(cargo, absRoot, "module", "Cargo.toml"), skipExisting)
	}
	for _, pom := range m.pomXMLs {
		boundaries = appendOrAugment(boundaries, boundaryAt(pom, absRoot, "module", "pom.xml"), skipExisting)
	}
	for _, gradle := range m.gradleBuilds {
		boundaries = appendOrAugment(boundaries, boundaryAt(gradle, absRoot, "module", filepath.Base(gradle)), skipExisting)
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

// boundaryAt builds a Boundary from a marker file's relative path.
func boundaryAt(markerPath, absRoot, kind, marker string) Boundary {
	dir := filepath.Dir(markerPath)
	return Boundary{
		Name:    boundaryName(dir, absRoot),
		Path:    dir,
		Type:    kind,
		Markers: []string{marker},
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

func inferTopology(s topologySignal) model.TopologyType {
	switch {
	case s.hasWorkspaceConfig(), s.goModCount > 1:
		return model.TopologyMonorepo
	case s.hasOrchestratedMicroservices(), s.dockerfileCount > 2:
		return model.TopologyMicroservice
	case s.cmdCount > 1, s.totalProjectMarkers > 2:
		return model.TopologyMonorepo
	case s.isSingleProjectMonolith():
		return model.TopologyMonolith
	}
	return model.TopologyUnknown
}
