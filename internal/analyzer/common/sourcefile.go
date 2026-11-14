package common

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SourceFile is a source file and the root of the scan it belongs to.
// Analyzers build module and endpoint IDs from it, so every file in a scan
// gets its own nodes.
type SourceFile struct {
	Root string // absolute scan root; "" when unknown
	Path string
}

// ModuleName is the file name without its extension.
func (f SourceFile) ModuleName() string {
	return strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path))
}

// ModuleID builds the node ID for the file's module.
//
// With a scan root the ID is the root-relative path without its extension
// ("mod:billing/app/views"), so every file gets its own node. Without one, or
// for a file outside the root, it falls back to the parent-folder form
// ("mod:app/views"), which collides for same-named files in same-named
// folders.
func (f SourceFile) ModuleID() string {
	if rel, ok := RelToRoot(f.Root, f.Path); ok {
		return "mod:" + strings.TrimSuffix(rel, filepath.Ext(rel))
	}
	return fmt.Sprintf("mod:%s/%s", filepath.Base(filepath.Dir(f.Path)), f.ModuleName())
}

// EndpointID builds the node ID for an endpoint declared on a 1-based line of
// the file: "endpoint:<root-relative path>:<line>". Without a scan root it
// falls back to the bare file name, which collides for same-named files in
// different folders.
func (f SourceFile) EndpointID(line int) string {
	name := filepath.Base(f.Path)
	if rel, ok := RelToRoot(f.Root, f.Path); ok {
		name = rel
	}
	return fmt.Sprintf("endpoint:%s:%d", name, line)
}
