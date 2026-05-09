package detector

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
