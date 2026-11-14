package java

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/olgasafonova/ridge/internal/analyzer/common"
)

// javaImport is one import declaration: the dotted name it names and whether
// it ends in ".*". `import static a.b.C.m;` has name "a.b.C.m".
type javaImport struct {
	name     string
	wildcard bool
}

// pkg is the package the import is labelled with: the name itself for a
// wildcard import, the name minus its class otherwise.
func (im javaImport) pkg() string {
	if im.wildcard {
		return im.name
	}
	return dropLastSegment(im.name)
}

// parseImport reads an import_declaration. ok is false when it names nothing.
func parseImport(node *sitter.Node, src []byte) (javaImport, bool) {
	var im javaImport
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "scoped_identifier", "identifier":
			im.name = strings.TrimSpace(child.Content(src))
		case "asterisk":
			im.wildcard = true
		}
	}
	return im, im.name != ""
}

// packageName returns the file's package declaration, or "" for the default
// package.
func packageName(root *sitter.Node, src []byte) string {
	for i := 0; i < int(root.ChildCount()); i++ {
		if decl := root.Child(i); decl.Type() == "package_declaration" {
			return declaredName(decl, src)
		}
	}
	return ""
}

// declaredName returns the dotted name a package declaration declares.
func declaredName(decl *sitter.Node, src []byte) string {
	for i := 0; i < int(decl.ChildCount()); i++ {
		if t := decl.Child(i).Type(); t == "scoped_identifier" || t == "identifier" {
			return decl.Child(i).Content(src)
		}
	}
	return ""
}

// sourceRootPatterns are the Maven and Gradle source-root layouts searched,
// relative to the scan root, for classes outside the importing file's own
// source root: "src/main/java" and "src/test/java" of a single-module build,
// and the same under each module of a multi-module one.
var sourceRootPatterns = []string{"src/*/java", "*/src/*/java"}

// importResolver maps imports of project classes to the module IDs of the
// files that declare them.
type importResolver struct {
	file    common.SourceFile
	ownRoot string // source root of the importing file
	roots   []string
	listed  bool
}

// newImportResolver prepares resolution for the file declaring pkg. The
// file's own source root is its folder minus the package path.
func newImportResolver(file common.SourceFile, pkg string) *importResolver {
	dir := filepath.Dir(file.Path)
	pkgDir := filepath.FromSlash(strings.ReplaceAll(pkg, ".", "/"))
	ownRoot := dir
	if trimmed, ok := strings.CutSuffix(dir, string(filepath.Separator)+pkgDir); ok && pkg != "" {
		ownRoot = trimmed
	}
	return &importResolver{file: file, ownRoot: ownRoot}
}

// target returns the edge target for an import: the module ID of the project
// file that declares the imported class, otherwise the unresolved
// "import:<package>" placeholder. Wildcard imports name a package, not a
// class, and stay unresolved.
func (r *importResolver) target(im javaImport) string {
	if !im.wildcard && r.file.Root != "" {
		if file, ok := r.classFile(im.name); ok {
			return common.SourceFile{Root: r.file.Root, Path: file}.ModuleID()
		}
	}
	return "import:" + im.pkg()
}

// classFile finds the file declaring a dotted class name. Trailing segments
// are dropped one at a time, so a nested class ("a.b.Outer.Inner") or a
// static member ("a.b.Util.helper") finds the file of its top-level class.
func (r *importResolver) classFile(name string) (string, bool) {
	segs := strings.Split(name, ".")
	for n := len(segs); n >= 2; n-- {
		rel := filepath.Join(segs[:n]...) + ".java"
		for _, root := range r.sourceRoots() {
			if file := filepath.Join(root, rel); r.isProjectFile(file) {
				return file, true
			}
		}
	}
	return "", false
}

// sourceRoots lists the importing file's own source root first, then the
// conventional ones under the scan root in sorted order. The list is built on
// first use, so files without project imports never touch the file system.
func (r *importResolver) sourceRoots() []string {
	if r.listed {
		return r.roots
	}
	r.listed = true
	r.roots = []string{r.ownRoot}
	var found []string
	for _, pattern := range sourceRootPatterns {
		matches, _ := filepath.Glob(filepath.Join(r.file.Root, pattern))
		found = append(found, matches...)
	}
	sort.Strings(found)
	for _, root := range found {
		if root != r.ownRoot {
			r.roots = append(r.roots, root)
		}
	}
	return r.roots
}

// isProjectFile reports whether path is a regular file inside the scan root.
func (r *importResolver) isProjectFile(path string) bool {
	if _, inside := common.RelToRoot(r.file.Root, path); !inside {
		return false
	}
	// The path comes from an import in scanned source and is only stat'ed.
	info, err := os.Stat(path) // #nosec G703 -- see comment above
	return err == nil && info.Mode().IsRegular()
}
