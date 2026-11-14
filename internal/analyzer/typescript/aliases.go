package typescript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/olgasafonova/ridge/internal/jsonc"
)

// Import aliases let a specifier such as "$lib/server/ai" or "#lib/trace.ts"
// stand for a path inside the project. Three sources declare them, read here
// in this order:
//
//   - tsconfig.json compilerOptions.paths, following relative "extends"
//   - package.json "imports" (Node subpath imports, keys starting with "#")
//   - SvelteKit's built-in "$lib" -> src/lib, when the project depends on
//     @sveltejs/kit. SvelteKit writes that alias into the generated
//     .svelte-kit/tsconfig.json, which a fresh checkout does not have. A $lib
//     declared in tsconfig is tried first.

// aliasRule maps one alias pattern to its candidate targets. Pattern and
// targets may each contain a single "*"; targets are absolute paths.
type aliasRule struct {
	pattern string
	targets []string
}

// aliasSet is a project's alias rules, most specific pattern first.
type aliasSet []aliasRule

// resolve returns the TypeScript source file spec points at through an alias.
func (s aliasSet) resolve(spec string) (string, bool) {
	for _, rule := range s {
		star, ok := matchAlias(rule.pattern, spec)
		if !ok {
			continue
		}
		for _, target := range rule.targets {
			if file, found := resolveModuleFile(strings.Replace(target, "*", star, 1)); found {
				return file, true
			}
		}
	}
	return "", false
}

// matchAlias reports whether spec matches pattern and returns the text the
// "*" stands for (empty for an exact pattern).
func matchAlias(pattern, spec string) (string, bool) {
	prefix, suffix, wildcard := strings.Cut(pattern, "*")
	if !wildcard {
		return "", pattern == spec
	}
	if len(spec) < len(prefix)+len(suffix) || !strings.HasPrefix(spec, prefix) || !strings.HasSuffix(spec, suffix) {
		return "", false
	}
	return spec[len(prefix) : len(spec)-len(suffix)], true
}

// sorted orders rules so the longest literal prefix wins, as TypeScript does.
func (s aliasSet) sorted() aliasSet {
	sort.SliceStable(s, func(i, j int) bool {
		return aliasPrefixLen(s[i].pattern) > aliasPrefixLen(s[j].pattern)
	})
	return s
}

func aliasPrefixLen(pattern string) int {
	prefix, _, _ := strings.Cut(pattern, "*")
	return len(prefix)
}

// aliasCache remembers parsed alias config per config file. An analyzer lives
// as long as the server does, so entries are revalidated against the file's
// modification time and size on every lookup.
type aliasCache struct {
	mu      sync.Mutex
	entries map[string]aliasEntry
}

type aliasEntry struct {
	modTime time.Time
	size    int64
	rules   aliasSet
}

func newAliasCache() *aliasCache {
	return &aliasCache{entries: make(map[string]aliasEntry)}
}

// forFile returns the alias rules that apply to a source file: those of the
// nearest tsconfig.json and the nearest package.json at or above its
// directory, searching no higher than the scan root.
func (c *aliasCache) forFile(root, file string) aliasSet {
	dir := filepath.Dir(file)
	var rules aliasSet
	if cfg, ok := nearestConfig(root, dir, "tsconfig.json"); ok {
		rules = append(rules, c.load(cfg, tsconfigAliases)...)
	}
	if pkg, ok := nearestConfig(root, dir, "package.json"); ok {
		rules = append(rules, c.load(pkg, packageAliases)...)
	}
	return rules.sorted()
}

// load returns the cached rules for a config file, re-parsing when the file
// changed since it was cached.
func (c *aliasCache) load(file string, parse func(string) aliasSet) aliasSet {
	info, err := os.Stat(file)
	if err != nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[file]; ok && e.modTime.Equal(info.ModTime()) && e.size == info.Size() {
		return e.rules
	}
	rules := parse(file)
	c.entries[file] = aliasEntry{modTime: info.ModTime(), size: info.Size(), rules: rules}
	return rules
}

// nearestConfig walks from dir up to root looking for a file called name.
func nearestConfig(root, dir, name string) (string, bool) {
	for {
		if _, inside := relToRoot(root, dir); !inside {
			return "", false
		}
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// maxExtendsDepth bounds an "extends" chain so a cycle cannot loop forever.
const maxExtendsDepth = 8

// tsconfigAliases reads compilerOptions.paths from a tsconfig.json.
func tsconfigAliases(file string) aliasSet {
	return tsconfigPaths(file, maxExtendsDepth)
}

// tsconfigPaths returns the paths a tsconfig declares, or failing that the
// paths of the first config it extends that declares any. TypeScript replaces
// an inherited "paths" wholesale rather than merging it, and resolves each
// target against the baseUrl, or the directory, of the config declaring it.
func tsconfigPaths(file string, depth int) aliasSet {
	var cfg struct {
		Extends         json.RawMessage `json:"extends"`
		CompilerOptions struct {
			BaseURL string              `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if !readJSONC(file, &cfg) {
		return nil
	}
	dir := filepath.Dir(file)
	if len(cfg.CompilerOptions.Paths) > 0 {
		base := filepath.Join(dir, cfg.CompilerOptions.BaseURL)
		rules := make(aliasSet, 0, len(cfg.CompilerOptions.Paths))
		for pattern, targets := range cfg.CompilerOptions.Paths {
			rules = append(rules, aliasRule{pattern: pattern, targets: joinAll(base, targets)})
		}
		return rules
	}
	if depth == 0 {
		return nil
	}
	for _, parent := range extendsList(cfg.Extends) {
		if !isRelativeSpec(parent) {
			continue // a config published in a package: not followed
		}
		parentFile := filepath.Join(dir, parent)
		if filepath.Ext(parentFile) != ".json" {
			parentFile += ".json"
		}
		if rules := tsconfigPaths(parentFile, depth-1); len(rules) > 0 {
			return rules
		}
	}
	return nil
}

// extendsList decodes "extends", which is a string or an array of strings.
// With an array, later entries take precedence, so they are returned first.
func extendsList(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return nil
	}
	for i, j := 0, len(many)-1; i < j; i, j = i+1, j-1 {
		many[i], many[j] = many[j], many[i]
	}
	return many
}

// packageAliases reads Node subpath imports from a package.json and adds
// SvelteKit's default $lib alias when the package depends on SvelteKit.
func packageAliases(file string) aliasSet {
	var pkg struct {
		Imports         map[string]json.RawMessage `json:"imports"`
		Dependencies    map[string]string          `json:"dependencies"`
		DevDependencies map[string]string          `json:"devDependencies"`
	}
	if !readJSONC(file, &pkg) {
		return nil
	}
	dir := filepath.Dir(file)
	var rules aliasSet
	for pattern, raw := range pkg.Imports {
		if target, ok := importTarget(raw); ok && strings.HasPrefix(pattern, "#") {
			rules = append(rules, aliasRule{pattern: pattern, targets: []string{filepath.Join(dir, target)}})
		}
	}
	_, dep := pkg.Dependencies["@sveltejs/kit"]
	_, devDep := pkg.DevDependencies["@sveltejs/kit"]
	if dep || devDep {
		lib := filepath.Join(dir, "src", "lib")
		rules = append(rules,
			aliasRule{pattern: "$lib", targets: []string{lib}},
			aliasRule{pattern: "$lib/*", targets: []string{filepath.Join(lib, "*")}},
		)
	}
	return rules
}

// importTarget extracts the path from a package.json "imports" value: a plain
// string, or a conditions object whose "default" (else "import") is a string.
// Only targets inside the package ("./...") are aliases to source files.
func importTarget(raw json.RawMessage) (string, bool) {
	var target string
	if json.Unmarshal(raw, &target) != nil {
		var conditions map[string]json.RawMessage
		if json.Unmarshal(raw, &conditions) != nil {
			return "", false
		}
		for _, key := range []string{"default", "import"} {
			if json.Unmarshal(conditions[key], &target) == nil && target != "" {
				break
			}
		}
	}
	return target, strings.HasPrefix(target, "./")
}

func readJSONC(file string, v any) bool {
	data, err := os.ReadFile(file) //nolint:gosec // G304: config file found by walking up from a scanned source file, inside the scan root
	if err != nil {
		return false
	}
	return json.Unmarshal(jsonc.Strip(data), v) == nil
}

func joinAll(base string, targets []string) []string {
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = filepath.Join(base, t)
	}
	return out
}
