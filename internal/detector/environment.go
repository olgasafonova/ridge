package detector

import (
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

	var violations []Violation
	_ = filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if d.IsDir() {
			if path != rootPath && envSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		switch d.Name() {
		case "go.mod":
			violations = append(violations, checkGoModReplacements(path, rootPath)...)
		case "tsconfig.json":
			violations = append(violations, checkTSConfig(path, rootPath)...)
		}
		return nil
	})
	return violations
}

// checkGoModReplacements flags replace directives whose filesystem path
// target (starting with ./ or ../, per the go.mod spec) doesn't exist.
// A missing replace target breaks every build of that module.
func checkGoModReplacements(goModPath, rootPath string) []Violation {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return nil
	}

	modDir := filepath.Dir(goModPath)
	relMod := relativizePath(goModPath, rootPath)

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
// tsconfig.json permits comments and trailing commas (JSONC), so the file is
// stripped before parsing; files that still fail to parse are skipped rather
// than reported — parse validity is the compiler's job, not ridge's.
func checkTSConfig(tsconfigPath, rootPath string) []Violation {
	data, err := os.ReadFile(tsconfigPath)
	if err != nil {
		return nil
	}
	var cfg tsConfig
	if err := json.Unmarshal(stripJSONC(data), &cfg); err != nil {
		return nil
	}

	confDir := filepath.Dir(tsconfigPath)
	relConf := relativizePath(tsconfigPath, rootPath)

	baseDir := confDir
	if cfg.CompilerOptions.BaseURL != "" {
		baseDir = filepath.Join(confDir, cfg.CompilerOptions.BaseURL)
		if _, err := os.Stat(baseDir); err != nil {
			// Everything under paths resolves against baseUrl; reporting
			// each alias on top of this would be N duplicates of one root
			// cause, so stop here.
			return []Violation{{
				Rule:     "tsconfig_baseurl_missing",
				Severity: model.SeverityHigh,
				Subject:  fmt.Sprintf("%s: baseUrl %s", relConf, cfg.CompilerOptions.BaseURL),
				Detail:   fmt.Sprintf("tsconfig baseUrl %q does not exist (resolved: %s); all path aliases resolve against it", cfg.CompilerOptions.BaseURL, baseDir),
			}}
		}
	}

	var violations []Violation
	for alias, patterns := range cfg.CompilerOptions.Paths {
		for _, pattern := range patterns {
			if missing, resolved := pathTargetMissing(baseDir, pattern); missing {
				violations = append(violations, Violation{
					Rule:     "tsconfig_path_target_missing",
					Severity: model.SeverityMedium,
					Subject:  fmt.Sprintf("%s: %s -> %s", relConf, alias, pattern),
					Detail:   fmt.Sprintf("tsconfig path alias %q maps to %q which does not exist (resolved: %s); imports through this alias will not resolve", alias, pattern, resolved),
				})
			}
		}
	}
	return violations
}

// pathTargetMissing reports whether a tsconfig paths pattern points at a
// missing location. For wildcard patterns like "src/lib/*" the directory
// prefix before the wildcard is checked; for exact patterns the full path.
func pathTargetMissing(baseDir, pattern string) (bool, string) {
	check := pattern
	if prefix, _, found := strings.Cut(pattern, "*"); found {
		check = strings.TrimSuffix(prefix, "/")
		if check == "" {
			return false, "" // pattern like "*" matches baseUrl itself
		}
	}
	resolved := filepath.Join(baseDir, check)
	if _, err := os.Stat(resolved); err != nil {
		return true, resolved
	}
	return false, resolved
}

// stripJSONC removes // and /* */ comments plus trailing commas from JSONC,
// preserving string contents, so encoding/json can parse tsconfig files.
// Two passes: comments first, then trailing commas — a single pass would
// miss a trailing comma separated from its closing brace by a comment
// (`"x", /* note */ }`), because the comma lookahead would see the comment.
func stripJSONC(data []byte) []byte {
	return stripTrailingCommas(stripComments(data))
}

func stripComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString, escaped := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			i += 2
			for i+1 < len(data) && (data[i] != '*' || data[i+1] != '/') {
				i++
			}
			i++ // skip the closing '/'
		default:
			out = append(out, c)
		}
	}
	return out
}

func stripTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString, escaped := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
			out = append(out, c)
		case ',':
			// Drop the comma if the next non-whitespace byte closes a
			// container (trailing comma).
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
