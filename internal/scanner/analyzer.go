// Package scanner provides the file walker and analyzer orchestration layer.
package scanner

import "github.com/olgasafonova/ridge/internal/model"

// Analyzer is implemented by each language-specific analyzer.
// An analyzer receives a file path and returns nodes and edges discovered in that file.
type Analyzer interface {
	// Analyze processes a single source file and returns architectural elements found in it.
	Analyze(path string) ([]*model.Node, []*model.Edge, error)

	// Extensions returns the file extensions this analyzer handles (e.g., ".go", ".ts").
	Extensions() []string

	// Language returns the language name for metadata.
	Language() string

	// Clone returns an independent copy safe for use in a separate goroutine.
	// Tree-sitter parsers are not thread-safe; each worker needs its own instance.
	Clone() Analyzer

	// Signature returns a stable identifier for the analyzer's behavior. Cached
	// results produced by a different signature are treated as stale even when
	// file content is unchanged. Bump the embedded version constant in the
	// analyzer whenever its behavior changes (new detectors, schema changes,
	// bug fixes that alter emitted Nodes/Edges).
	Signature() string
}

// RootedAnalyzer is an optional extension for analyzers whose output depends
// on where a file sits inside the scanned tree: node IDs built from the
// root-relative path, or imports resolved against sibling files. The scanner
// calls AnalyzeRooted instead of Analyze when an analyzer implements it.
// root is the absolute scan root; it is passed per call rather than stored so
// one analyzer instance can serve scans of different roots.
type RootedAnalyzer interface {
	AnalyzeRooted(root, path string) ([]*model.Node, []*model.Edge, error)
}

// analyzeFile runs a on path, preferring the root-aware entry point.
func analyzeFile(a Analyzer, root, path string) ([]*model.Node, []*model.Edge, error) {
	if ra, ok := a.(RootedAnalyzer); ok {
		return ra.AnalyzeRooted(root, path)
	}
	return a.Analyze(path)
}
