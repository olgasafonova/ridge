package detector

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/olgasafonova/ridge/internal/jsonc"
	"github.com/olgasafonova/ridge/internal/model"
)

// Environment inconsistency checks: validate build/module configuration
// against the filesystem it describes. Inspired by dependency-cruiser 18.1.0's
// environment inconsistency checks (typescript/babel config drift). The
// pattern: declared module resolution that disagrees with reality breaks
// builds in ways the import graph alone can't show.
//
// v1 scope is config-vs-filesystem existence checks:
//   - go.mod replace directives whose local path target doesn't exist
//   - tsconfig.json baseUrl pointing at a missing directory
//   - tsconfig.json compilerOptions.paths targets pointing at missing locations

// Domain types, following the relPath/rootPrefix precedent in
// model/graph_resolver.go: distinct from generic strings so signatures carry
// the domain language and the compiler catches role mix-ups (for example,
// passing the scan root where a config file path is expected).

// configFile is the absolute path of a build-configuration file
// (go.mod, tsconfig.json) found during the walk.
type configFile string

// goModSource is the raw contents of a go.mod file.
type goModSource string

// localTarget is a replace directive's filesystem target (./ or ../ prefix).
type localTarget string

// pathAlias is a tsconfig paths alias key (for example "@lib/*").
type pathAlias string

// aliasPattern is one tsconfig paths target pattern (may contain '*').
type aliasPattern string

// envSkipDirs mirrors the scanner's default skip set (scanner.go) so
// environment checks never wander into dirs the scan itself ignores.
var envSkipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	".next":        true,
	".nuxt":        true,
	".svelte-kit":  true,
	"target":       true,
}

// CheckEnvironment walks rootPath for build configuration files and returns
// violations where declared module resolution disagrees with the filesystem.
// Returns nil when rootPath is empty or not a readable directory (synthetic
// graphs in tests, snapshots restored from JSON).
func CheckEnvironment(rootPath string) []Violation {
	if rootPath == "" {
		return nil
	}
	info, err := os.Stat(rootPath)
	if err != nil || !info.IsDir() {
		return nil
	}
	c := &envChecker{root: rootPath}
	_ = filepath.WalkDir(rootPath, c.visit)
	return c.violations
}

// envChecker accumulates environment violations during the config-file walk.
type envChecker struct {
	root       string
	violations []Violation
}

// visit prunes skipped directories and dispatches each recognized config
// file to its checker. Unreadable entries are skipped, not fatal.
func (c *envChecker) visit(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return nil
	}
	if d.IsDir() {
		if path != c.root && envSkipDirs[d.Name()] {
			return filepath.SkipDir
		}
		return nil
	}
	switch d.Name() {
	case "go.mod":
		c.violations = append(c.violations, c.checkGoModReplacements(configFile(path))...)
	case "tsconfig.json":
		c.violations = append(c.violations, c.checkTSConfig(configFile(path))...)
	}
	return nil
}

// checkGoModReplacements flags replace directives whose filesystem path
// target (starting with ./ or ../, per the go.mod spec) doesn't exist.
// A missing replace target breaks every build of that module.
func (c *envChecker) checkGoModReplacements(file configFile) []Violation {
	data, err := os.ReadFile(string(file))
	if err != nil {
		return nil
	}

	modDir := filepath.Dir(string(file))
	relMod := relativizePath(string(file), c.root)

	var violations []Violation
	for _, target := range parseGoModLocalReplacements(goModSource(data)) {
		resolved := filepath.Join(modDir, string(target))
		if _, err := os.Stat(resolved); err == nil {
			continue
		}
		violations = append(violations, Violation{
			Rule:     "go_mod_replace_target_missing",
			Severity: model.SeverityHigh,
			Subject:  fmt.Sprintf("%s: %s", relMod, target),
			Detail:   fmt.Sprintf("go.mod replace directive targets %q which does not exist (resolved: %s); builds of this module will fail", string(target), resolved),
		})
	}
	return violations
}

// parseGoModLocalReplacements extracts filesystem path targets from replace
// directives, handling both single-line and block form. Only targets starting
// with ./ or ../ are returned — module-path replacements resolve through the
// module proxy and are not filesystem claims.
func parseGoModLocalReplacements(src goModSource) []localTarget {
	var targets []localTarget
	inBlock := false
	for line := range strings.Lines(string(src)) {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		switch {
		case inBlock:
			if line == ")" {
				inBlock = false
				continue
			}
			targets = appendLocalTarget(targets, line)
		case line == "replace (":
			inBlock = true
		case strings.HasPrefix(line, "replace "):
			targets = appendLocalTarget(targets, strings.TrimPrefix(line, "replace "))
		}
	}
	return targets
}

// appendLocalTarget parses one "old [version] => new [version]" directive and
// appends the RHS when it is a filesystem path (./ or ../ prefix).
func appendLocalTarget(targets []localTarget, directive string) []localTarget {
	_, rhs, found := strings.Cut(directive, "=>")
	if !found {
		return targets
	}
	fields := strings.Fields(rhs)
	if len(fields) == 0 {
		return targets
	}
	target := strings.Trim(fields[0], `"`)
	if strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../") {
		return append(targets, localTarget(target))
	}
	return targets
}

// compilerOptions is the subset of tsconfig compilerOptions relevant to
// module resolution.
type compilerOptions struct {
	BaseURL string              `json:"baseUrl"`
	Paths   map[string][]string `json:"paths"`
}

// tsConfig is the subset of tsconfig.json relevant to module resolution.
type tsConfig struct {
	CompilerOptions compilerOptions `json:"compilerOptions"`
}

// checkTSConfig flags baseUrl and paths targets that don't exist on disk.
func (c *envChecker) checkTSConfig(file configFile) []Violation {
	cfg, ok := loadTSConfig(file)
	if !ok {
		return nil
	}
	ctx := &tsContext{
		relConf: relativizePath(string(file), c.root),
		baseDir: filepath.Dir(string(file)),
	}
	if violations, fatal := ctx.applyBaseURL(cfg.CompilerOptions); fatal {
		return violations
	}
	return ctx.checkPaths(cfg.CompilerOptions.Paths)
}

// loadTSConfig reads and parses a tsconfig.json. tsconfig permits comments
// and trailing commas (JSONC), so the bytes are stripped before parsing;
// files that still fail to parse are skipped rather than reported — parse
// validity is the compiler's job, not ridge's.
func loadTSConfig(file configFile) (tsConfig, bool) {
	var cfg tsConfig
	data, err := os.ReadFile(string(file))
	if err != nil {
		return cfg, false
	}
	if err := json.Unmarshal(jsonc.Strip(data), &cfg); err != nil {
		return cfg, false
	}
	return cfg, true
}

// tsContext carries the per-tsconfig state that path checks resolve against.
type tsContext struct {
	relConf string // tsconfig path relative to the scan root, for Subjects
	baseDir string // directory that path aliases resolve against
}

// applyBaseURL resolves baseUrl into the context. A missing baseUrl dir is
// fatal for the whole config: every path alias resolves against it, so
// reporting each alias would be N duplicates of one root cause.
func (c *tsContext) applyBaseURL(opts compilerOptions) ([]Violation, bool) {
	if opts.BaseURL == "" {
		return nil, false
	}
	resolved := filepath.Join(c.baseDir, opts.BaseURL)
	if _, err := os.Stat(resolved); err == nil {
		c.baseDir = resolved
		return nil, false
	}
	return []Violation{{
		Rule:     "tsconfig_baseurl_missing",
		Severity: model.SeverityHigh,
		Subject:  fmt.Sprintf("%s: baseUrl %s", c.relConf, opts.BaseURL),
		Detail:   fmt.Sprintf("tsconfig baseUrl %q does not exist (resolved: %s); all path aliases resolve against it", opts.BaseURL, resolved),
	}}, true
}

// checkPaths flags path aliases whose targets don't exist on disk.
func (c *tsContext) checkPaths(paths map[string][]string) []Violation {
	var violations []Violation
	for alias, patterns := range paths {
		for _, pattern := range patterns {
			if v, missing := c.pathViolation(pathAlias(alias), aliasPattern(pattern)); missing {
				violations = append(violations, v)
			}
		}
	}
	return violations
}

// pathViolation checks one alias pattern. For wildcard patterns like
// "src/lib/*" the directory prefix before the wildcard is checked; for
// exact patterns the full path.
func (c *tsContext) pathViolation(alias pathAlias, pattern aliasPattern) (Violation, bool) {
	check := string(pattern)
	if prefix, _, found := strings.Cut(check, "*"); found {
		check = strings.TrimSuffix(prefix, "/")
		if check == "" {
			return Violation{}, false // pattern like "*" matches baseUrl itself
		}
	}
	resolved := filepath.Join(c.baseDir, check)
	if _, err := os.Stat(resolved); err == nil {
		return Violation{}, false
	}
	return Violation{
		Rule:     "tsconfig_path_target_missing",
		Severity: model.SeverityMedium,
		Subject:  fmt.Sprintf("%s: %s -> %s", c.relConf, alias, pattern),
		Detail:   fmt.Sprintf("tsconfig path alias %q maps to %q which does not exist (resolved: %s); imports through this alias will not resolve", string(alias), string(pattern), resolved),
	}, true
}
