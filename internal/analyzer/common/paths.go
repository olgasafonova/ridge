package common

import (
	"path/filepath"
	"strings"
)

// RelToRoot returns path relative to root with forward slashes. ok is false
// when root is empty or path is not inside it. Analyzers build node IDs from
// the result so every file in a scan gets its own ID.
func RelToRoot(root, path string) (string, bool) {
	if root == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || leavesRoot(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// leavesRoot reports whether a relative path climbs out of its base.
func leavesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
