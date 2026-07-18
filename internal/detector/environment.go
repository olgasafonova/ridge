package detector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
		c.violations = append(c.violations, c.checkGoModReplacements(path)...)
	case "tsconfig.json":
		c.violations = append(c.violations, c.checkTSConfig(path)...)
	}
	return nil
}

// checkGoModReplacements flags replace directives whose filesystem path
// target (starting with ./ or ../, per the go.mod spec) doesn't exist.
// A missing replace target breaks every build of that module.
func (c *envChecker) checkGoModReplacements(goModPath string) []Violation {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return nil
	}

	modDir := filepath.Dir(goModPath)
	relMod := relativizePath(goModPath, c.root)

	var violations []Violation
	for _, target := range parseGoModLocalReplacements(string(data)) {
		resolved := filepath.Join(modDir, target)
		if _, err := os.Stat(resolved); err == nil {
			continue
		}
		violations = append(violations, Violation{
			Rule:     "go_mod_replace_target_missing",
			Severity: model.SeverityHigh,
			Subject:  fmt.Sprintf("%s: %s", relMod, target),
			Detail:   fmt.Sprintf("go.mod replace directive targets %q which does not exist (resolved: %s); builds of this module will fail", target, resolved),
		})
	}
	return violations
}

// parseGoModLocalReplacements extracts filesystem path targets from replace
// directives, handling both single-line and block form. Only targets starting
// with ./ or ../ are returned — module-path replacements resolve through the
// module proxy and are not filesystem claims.
func parseGoModLocalReplacements(content string) []string {
	var targets []string
	inBlock := false
	for line := range strings.Lines(content) {
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
func appendLocalTarget(targets []string, directive string) []string {
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
		return append(targets, target)
	}
	return targets
}

// tsConfig is the subset of tsconfig.json relevant to module resolution.
type tsConfig struct {
	CompilerOptions struct {
		BaseURL string              `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	} `json:"compilerOptions"`
}

// checkTSConfig flags baseUrl and paths targets that don't exist on disk.
func (c *envChecker) checkTSConfig(tsconfigPath string) []Violation {
	cfg, ok := loadTSConfig(tsconfigPath)
	if !ok {
		return nil
	}
	ctx := &tsContext{
		relConf: relativizePath(tsconfigPath, c.root),
		baseDir: filepath.Dir(tsconfigPath),
	}
	if violations, fatal := ctx.applyBaseURL(cfg.CompilerOptions.BaseURL); fatal {
		return violations
	}
	return ctx.checkPaths(cfg.CompilerOptions.Paths)
}

// loadTSConfig reads and parses a tsconfig.json. tsconfig permits comments
// and trailing commas (JSONC), so the bytes are stripped before parsing;
// files that still fail to parse are skipped rather than reported — parse
// validity is the compiler's job, not ridge's.
func loadTSConfig(path string) (tsConfig, bool) {
	var cfg tsConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, false
	}
	if err := json.Unmarshal(stripJSONC(data), &cfg); err != nil {
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
func (c *tsContext) applyBaseURL(baseURL string) ([]Violation, bool) {
	if baseURL == "" {
		return nil, false
	}
	resolved := filepath.Join(c.baseDir, baseURL)
	if _, err := os.Stat(resolved); err == nil {
		c.baseDir = resolved
		return nil, false
	}
	return []Violation{{
		Rule:     "tsconfig_baseurl_missing",
		Severity: model.SeverityHigh,
		Subject:  fmt.Sprintf("%s: baseUrl %s", c.relConf, baseURL),
		Detail:   fmt.Sprintf("tsconfig baseUrl %q does not exist (resolved: %s); all path aliases resolve against it", baseURL, resolved),
	}}, true
}

// checkPaths flags path aliases whose targets don't exist on disk.
func (c *tsContext) checkPaths(paths map[string][]string) []Violation {
	var violations []Violation
	for alias, patterns := range paths {
		for _, pattern := range patterns {
			if v, missing := c.pathViolation(alias, pattern); missing {
				violations = append(violations, v)
			}
		}
	}
	return violations
}

// pathViolation checks one alias pattern. For wildcard patterns like
// "src/lib/*" the directory prefix before the wildcard is checked; for
// exact patterns the full path.
func (c *tsContext) pathViolation(alias, pattern string) (Violation, bool) {
	check := pattern
	if prefix, _, found := strings.Cut(pattern, "*"); found {
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
		Detail:   fmt.Sprintf("tsconfig path alias %q maps to %q which does not exist (resolved: %s); imports through this alias will not resolve", alias, pattern, resolved),
	}, true
}

// stripJSONC removes // and /* */ comments plus trailing commas from JSONC,
// preserving string contents, so encoding/json can parse tsconfig files.
// Two passes: comments first, then trailing commas — a single pass would
// miss a trailing comma separated from its closing brace by a comment
// (`"x", /* note */ }`), because the comma lookahead would see the comment.
func stripJSONC(data []byte) []byte {
	return stripTrailingCommas(stripComments(data))
}

// stripComments removes // and /* */ comments outside string literals.
func stripComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		switch {
		case data[i] == '"':
			out, i = copyJSONString(data, i, out)
		case lookingAt(data, i, '/', '/'):
			i = skipLineComment(data, i)
		case lookingAt(data, i, '/', '*'):
			i = skipBlockComment(data, i)
		default:
			out = append(out, data[i])
			i++
		}
	}
	return out
}

// stripTrailingCommas removes commas whose next non-whitespace byte closes
// a container, outside string literals.
func stripTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		switch {
		case data[i] == '"':
			out, i = copyJSONString(data, i, out)
		case data[i] == ',' && nextClosesContainer(data, i+1):
			i++ // trailing comma: drop it
		default:
			out = append(out, data[i])
			i++
		}
	}
	return out
}

// copyJSONString appends the string literal starting at data[i] (an opening
// quote) to out, returning the new output and the index past the closing
// quote. Escaped characters, including \", are copied verbatim.
func copyJSONString(data []byte, i int, out []byte) ([]byte, int) {
	out = append(out, data[i])
	for i++; i < len(data); {
		ch := data[i]
		out = append(out, ch)
		i++
		if ch == '\\' && i < len(data) {
			out = append(out, data[i])
			i++
			continue
		}
		if ch == '"' {
			break
		}
	}
	return out, i
}

// lookingAt reports whether data[i] and data[i+1] are the given byte pair.
func lookingAt(data []byte, i int, first, second byte) bool {
	if data[i] != first {
		return false
	}
	return i+1 < len(data) && data[i+1] == second
}

// skipLineComment advances past a // comment, leaving the terminating
// newline (if any) for the caller to copy.
func skipLineComment(data []byte, i int) int {
	if j := bytes.IndexByte(data[i:], '\n'); j >= 0 {
		return i + j
	}
	return len(data)
}

// skipBlockComment advances past a /* */ comment, including the closer.
// An unterminated comment consumes the rest of the input.
func skipBlockComment(data []byte, i int) int {
	if j := bytes.Index(data[i+2:], []byte("*/")); j >= 0 {
		return i + 2 + j + 2
	}
	return len(data)
}

// nextClosesContainer reports whether the next non-whitespace byte at or
// after i closes a JSON container, making a preceding comma a trailing one.
func nextClosesContainer(data []byte, i int) bool {
	for i < len(data) && isJSONSpace(data[i]) {
		i++
	}
	if i >= len(data) {
		return false
	}
	return data[i] == '}' || data[i] == ']'
}

func isJSONSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}
