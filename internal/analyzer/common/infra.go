// Package common provides shared utilities for language analyzers.
package common

import (
	"net/url"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/olgasafonova/ridge/internal/model"
)

// InfraPattern defines an infrastructure import pattern with the node/edge to create.
type InfraPattern struct {
	Packages []string
	NodeType model.NodeType
	EdgeType model.EdgeType
	NodeID   string
	NodeName string
}

// ClassifyImport checks if importPath matches any infrastructure pattern
// and returns the corresponding infrastructure node and edge.
// separator is "/" for TypeScript (e.g. "pg/pool") or "." for Python (e.g. "sqlalchemy.orm").
func ClassifyImport(importPath, modID string, patterns []InfraPattern, separator string) ([]*model.Node, []*model.Edge) {
	for _, pattern := range patterns {
		if MatchesAny(importPath, pattern.Packages, separator) {
			nodes := []*model.Node{{
				ID:   pattern.NodeID,
				Name: pattern.NodeName,
				Type: pattern.NodeType,
				Properties: map[string]string{
					"detected_via": importPath,
				},
			}}
			edges := []*model.Edge{{
				Source:     modID,
				Target:     pattern.NodeID,
				Type:       pattern.EdgeType,
				Label:      pattern.NodeName,
				Confidence: 0.8,
			}}
			return nodes, edges
		}
	}
	return nil, nil
}

// MatchesAny checks if importPath matches any of the patterns.
// Matches exact name or as a prefix with the given separator.
func MatchesAny(importPath string, patterns []string, separator string) bool {
	for _, p := range patterns {
		if importPath == p || strings.HasPrefix(importPath, p+separator) {
			return true
		}
	}
	return false
}

// ParseServiceFromURL extracts the host from a URL string.
// Returns empty string if the URL is relative, has no host, or uses a non-HTTP scheme.
func ParseServiceFromURL(rawURL string) (host string, ok bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	h := u.Hostname()
	if h == "" {
		return "", false
	}
	return h, true
}

// WalkTree performs a pre-order depth-first traversal of a tree-sitter node,
// calling fn for each node encountered. Iterative: a recursive walk overflows
// the goroutine stack on pathologically nested source (e.g. millions of nested
// parens), and stack overflow is `runtime.throw` — unrecoverable, kills the
// scan and every in-flight tool.
func WalkTree(node *sitter.Node, fn func(*sitter.Node)) {
	if node == nil {
		return
	}
	stack := []*sitter.Node{node}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		fn(n)
		// Push children in reverse so the leftmost child is popped first,
		// preserving the pre-order semantics callers depended on.
		for i := int(n.ChildCount()) - 1; i >= 0; i-- {
			stack = append(stack, n.Child(i))
		}
	}
}
