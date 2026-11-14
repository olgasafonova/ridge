package typescript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// moduleName is the file name without its final extension ("app" for app.ts).
func moduleName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// moduleID builds the node ID for the module at path.
//
// With a scan root the ID is the root-relative path without its extension
// ("mod:src/lib/server/ai/ports/outbound/index"), so every file gets its own
// node. Without one, or for a file outside the root, it falls back to the
// parent-folder form ("mod:outbound/index"), which collides for same-named
// files in same-named folders.
func moduleID(root, path string) string {
	if rel, ok := relToRoot(root, path); ok {
		return "mod:" + strings.TrimSuffix(rel, filepath.Ext(rel))
	}
	return fmt.Sprintf("mod:%s/%s", filepath.Base(filepath.Dir(path)), moduleName(path))
}

// relToRoot returns path relative to root with forward slashes. ok is false
// when root is empty or path is not inside it.
func relToRoot(root, path string) (string, bool) {
	if root == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// importer identifies the file whose imports are being resolved.
type importer struct {
	root    string // absolute scan root; "" when unknown
	file    string // absolute path of the importing file
	modID   string
	aliases aliasSet // import aliases in force for file
}

// importerFor describes the file at path for import resolution. Alias config
// is only looked up when a scan root bounds the search for it.
func (a *Analyzer) importerFor(root, path, modID string) importer {
	im := importer{root: root, file: path, modID: modID}
	if root != "" {
		im.aliases = a.aliases.forFile(root, path)
	}
	return im
}

// target returns the edge target for an import specifier: the module ID of
// the file it points at when that file is a TypeScript module inside the scan
// root, otherwise the unresolved "import:<specifier>" placeholder.
func (im importer) target(spec string) string {
	if file, ok := im.sourceFile(spec); ok {
		if _, inside := relToRoot(im.root, file); inside {
			return moduleID(im.root, file)
		}
	}
	return "import:" + spec
}

// sourceFile finds the TypeScript file spec refers to: next to the importing
// file for a relative specifier, through the project's aliases otherwise.
func (im importer) sourceFile(spec string) (string, bool) {
	switch {
	case im.root == "":
		return "", false
	case isRelativeSpec(spec):
		return resolveModuleFile(filepath.Join(filepath.Dir(im.file), spec))
	default:
		return im.aliases.resolve(spec)
	}
}

func isRelativeSpec(spec string) bool {
	return spec == "." || spec == ".." || strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")
}

// jsToTS maps the JavaScript extensions TypeScript sources write in import
// specifiers ("./crs.js") to the source extensions they compile from.
var jsToTS = map[string][]string{
	".js":  {".ts", ".tsx"},
	".jsx": {".tsx"},
	".mjs": {".mts"},
	".cjs": {".cts"},
}

// resolveModuleFile finds the TypeScript source file an extensionless or
// .js-suffixed import base refers to, following the order TypeScript's
// bundler resolution uses: the path itself, source extensions, then a
// directory index. Files this analyzer does not parse (.svelte, .json, .css)
// are not matched, so those imports stay unresolved.
func resolveModuleFile(base string) (string, bool) {
	var candidates []string
	ext := filepath.Ext(base)
	switch {
	case ext == ".ts" || ext == ".tsx":
		candidates = append(candidates, base)
	case jsToTS[ext] != nil:
		stem := strings.TrimSuffix(base, ext)
		for _, tsExt := range jsToTS[ext] {
			candidates = append(candidates, stem+tsExt)
		}
	}
	candidates = append(candidates,
		base+".ts", base+".tsx",
		filepath.Join(base, "index.ts"), filepath.Join(base, "index.tsx"),
	)
	for _, c := range candidates {
		// The candidate comes from an import specifier in scanned source and is
		// only stat'ed; the caller rejects results outside the scan root.
		info, err := os.Stat(c) // #nosec G703 -- see comment above
		if err == nil && info.Mode().IsRegular() {
			return c, true
		}
	}
	return "", false
}
