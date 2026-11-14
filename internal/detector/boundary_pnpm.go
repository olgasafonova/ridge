package detector

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxPnpmWorkspaceBytes caps how much of a pnpm-workspace.yaml is read. Real
// files are a few hundred bytes.
const maxPnpmWorkspaceBytes = 1 << 20

// pnpmWorkspaceListsPackages reports whether a pnpm-workspace.yaml declares
// workspace packages. pnpm also reads settings such as onlyBuiltDependencies
// and overrides from this file, so a single-package repo can have one with no
// "packages" list. A file that cannot be read or parsed lists nothing.
func pnpmWorkspaceListsPackages(path string) bool {
	f, err := os.Open(filepath.Clean(path)) // #nosec G304 -- path was found by walking the scanned project
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, maxPnpmWorkspaceBytes))
	if err != nil {
		return false
	}
	var workspace struct {
		Packages []string `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &workspace); err != nil {
		return false
	}
	for _, pattern := range workspace.Packages {
		if strings.TrimSpace(pattern) != "" {
			return true
		}
	}
	return false
}
