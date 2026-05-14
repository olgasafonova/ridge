// Package rust provides Rust analysis using tree-sitter.
// Detects use-declarations, HTTP endpoints (actix-web, axum, rocket, warp),
// database/queue/cache infrastructure, and outbound HTTP client calls.
package rust

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/rust"

	"github.com/olgasafonova/ridge/internal/analyzer/common"
	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/scanner"
)

// Analyzer implements the scanner.Analyzer interface for Rust files.
type Analyzer struct {
	parser *sitter.Parser
}

// New creates a Rust analyzer with a tree-sitter parser.
func New() *Analyzer {
	p := sitter.NewParser()
	p.SetLanguage(rust.GetLanguage())
	return &Analyzer{parser: p}
}

// Extensions returns Rust file extensions.
func (a *Analyzer) Extensions() []string {
	return []string{".rs"}
}

// Language returns "rust".
func (a *Analyzer) Language() string {
	return "rust"
}

// Clone returns an independent copy with a fresh tree-sitter parser.
// Tree-sitter parsers are not thread-safe; each goroutine needs its own.
func (a *Analyzer) Clone() scanner.Analyzer {
	return New()
}

// rustAnalyzerVersion is the cache-invalidation key for this analyzer. Bump it
// when emitted Nodes/Edges change shape or when detector logic changes.
const rustAnalyzerVersion = "rust-v1"

// Signature returns the analyzer's behavior version used for cache invalidation.
func (a *Analyzer) Signature() string {
	return rustAnalyzerVersion
}

// maxFileBytes caps source size before parsing. Files past the cap return a
// module stub with skipped=size, matching the other tree-sitter analyzers.
const maxFileBytes = 5 * 1024 * 1024 // 5 MB

// Analyze parses a Rust file and extracts architectural elements.
func (a *Analyzer) Analyze(path string) ([]*model.Node, []*model.Edge, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() > maxFileBytes {
		return []*model.Node{moduleSkippedForSize(path, info.Size())}, nil, nil
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}

	tree, err := a.parser.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	defer tree.Close()
	root := tree.RootNode()

	dir := filepath.Base(filepath.Dir(path))
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	modID := fmt.Sprintf("mod:%s/%s", dir, base)

	nodes := []*model.Node{{
		ID:       modID,
		Name:     base,
		Type:     model.NodeModule,
		Language: "rust",
		Path:     filepath.Dir(path),
	}}
	var edges []*model.Edge

	importNodes, importEdges := extractImports(root, src, modID)
	nodes = append(nodes, importNodes...)
	edges = append(edges, importEdges...)

	rc := routeContext{
		src:       src,
		modID:     modID,
		filePath:  path,
		framework: detectFramework(root, src),
	}

	routeNodes, routeEdges := extractRoutes(root, rc)
	nodes = append(nodes, routeNodes...)
	edges = append(edges, routeEdges...)

	callNodes, callEdges := extractHTTPCalls(root, src, modID)
	nodes = append(nodes, callNodes...)
	edges = append(edges, callEdges...)

	return nodes, edges, nil
}

// extractImports walks `use foo::bar::Baz;` declarations and emits dependency
// edges plus any infra classification matches.
func extractImports(root *sitter.Node, src []byte, modID string) ([]*model.Node, []*model.Edge) {
	var nodes []*model.Node
	var edges []*model.Edge
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() != "use_declaration" {
			continue
		}
		importPath := extractUsePath(child, src)
		if importPath == "" {
			continue
		}
		edges = append(edges, &model.Edge{
			Source:     modID,
			Target:     "import:" + importPath,
			Type:       model.EdgeDependency,
			Label:      importPath,
			Confidence: 0.9,
		})
		infraNodes, infraEdges := common.ClassifyImport(importPath, modID, infraPatterns, "::")
		nodes = append(nodes, infraNodes...)
		edges = append(edges, infraEdges...)
	}
	return nodes, edges
}

// extractUsePath returns the colon-colon-separated root of a use-declaration.
//
//	use foo;                  → "foo"
//	use foo::bar::Baz;        → "foo::bar"    (strip the imported name)
//	use foo::bar::{Baz, Qux}; → "foo::bar"    (group imports already stop at the prefix)
//	use foo::{a, b};          → "foo"
func extractUsePath(useNode *sitter.Node, src []byte) string {
	for i := 0; i < int(useNode.ChildCount()); i++ {
		child := useNode.Child(i)
		switch child.Type() {
		case "scoped_identifier":
			return dropLastSegment(child.Content(src))
		case "scoped_use_list":
			return groupedUsePrefix(child, src)
		case "use_list":
			return stripBraceGroup(child.Content(src))
		case "identifier":
			return child.Content(src)
		}
	}
	return ""
}

// dropLastSegment removes the trailing `::Segment` from a scoped path.
// "foo::bar::Baz" → "foo::bar"; "foo" → "foo".
func dropLastSegment(path string) string {
	path = strings.TrimSpace(path)
	if idx := strings.LastIndex(path, "::"); idx != -1 {
		return path[:idx]
	}
	return path
}

// groupedUsePrefix extracts the path prefix of a scoped_use_list node, which
// wraps shapes like `foo::bar::{Baz, Qux}`. Tree-sitter exposes the prefix as
// the `path` child field; when present we use it directly.
func groupedUsePrefix(node *sitter.Node, src []byte) string {
	if p := node.ChildByFieldName("path"); p != nil {
		return strings.TrimSpace(p.Content(src))
	}
	return stripBraceGroup(node.Content(src))
}

// stripBraceGroup trims a trailing `{...}` and any leftover `::` separator.
func stripBraceGroup(raw string) string {
	if brace := strings.Index(raw, "{"); brace != -1 {
		raw = raw[:brace]
	}
	return strings.TrimSuffix(strings.TrimSpace(raw), "::")
}

// infraPatterns defines Rust crate prefixes for infrastructure detection.
var infraPatterns = []common.InfraPattern{
	{
		Packages: []string{"sqlx", "diesel", "mongodb", "postgres", "tokio_postgres", "mysql", "rusqlite", "sea_orm", "sled"},
		NodeType: model.NodeDatabase,
		EdgeType: model.EdgeReadWrite,
		NodeID:   "infra:database",
		NodeName: "Database",
	},
	{
		Packages: []string{"lapin", "rdkafka", "nats", "rumqttc", "amiquip"},
		NodeType: model.NodeQueue,
		EdgeType: model.EdgePublish,
		NodeID:   "infra:queue",
		NodeName: "Message Queue",
	},
	{
		Packages: []string{"redis", "memcache", "moka", "cached"},
		NodeType: model.NodeCache,
		EdgeType: model.EdgeReadWrite,
		NodeID:   "infra:cache",
		NodeName: "Cache",
	},
	{
		Packages: []string{"reqwest", "hyper", "isahc", "ureq", "surf"},
		NodeType: model.NodeExternalAPI,
		EdgeType: model.EdgeAPICall,
		NodeID:   "infra:external_api",
		NodeName: "External API",
	},
}

// detectFramework inspects use-declarations to identify the HTTP framework.
func detectFramework(root *sitter.Node, src []byte) string {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() != "use_declaration" {
			continue
		}
		importPath := extractUsePath(child, src)
		switch {
		case strings.HasPrefix(importPath, "actix_web"):
			return "actix-web"
		case strings.HasPrefix(importPath, "axum"):
			return "axum"
		case strings.HasPrefix(importPath, "rocket"):
			return "rocket"
		case strings.HasPrefix(importPath, "warp"):
			return "warp"
		}
	}
	return "unknown"
}

// httpRouteMacros are the route-defining attribute macros recognized in
// actix-web and rocket: `#[get("/path")]`, `#[post("/path")]`, etc.
var httpRouteMacros = map[string]string{
	"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE",
	"patch": "PATCH", "head": "HEAD", "options": "OPTIONS",
}

// routeContext bundles the per-file inputs route extraction needs so the
// function signature stays below the Go arg threshold.
type routeContext struct {
	src       []byte
	modID     string
	filePath  string
	framework string
}

// extractRoutes finds attribute-based route definitions like `#[get("/users")]`.
// Reads the macro name (get/post/...) and the first string literal as the path.
func extractRoutes(root *sitter.Node, rc routeContext) ([]*model.Node, []*model.Edge) {
	var nodes []*model.Node
	var edges []*model.Edge

	common.WalkTree(root, func(node *sitter.Node) {
		if node.Type() != "attribute_item" {
			return
		}
		macro, path, ok := parseRouteAttribute(node, rc.src)
		if !ok {
			return
		}
		line := int(node.StartPoint().Row) + 1
		endpointID := fmt.Sprintf("endpoint:%s:%d", filepath.Base(rc.filePath), line)
		nodes = append(nodes, &model.Node{
			ID:       endpointID,
			Name:     fmt.Sprintf("%s %s", macro, path),
			Type:     model.NodeEndpoint,
			Language: "rust",
			Path:     rc.filePath,
			Properties: map[string]string{
				"method":    macro,
				"route":     path,
				"line":      fmt.Sprintf("%d", line),
				"framework": rc.framework,
			},
		})
		edges = append(edges, &model.Edge{
			Source:     rc.modID,
			Target:     endpointID,
			Type:       model.EdgeAPICall,
			Label:      "serves",
			Confidence: 0.85,
		})
	})

	return nodes, edges
}

// parseRouteAttribute recognizes a `#[get("/path")]`-shaped attribute_item and
// returns the method label and path string. Looks inside `attribute` →
// `identifier` (the macro name) plus `token_tree` (the args).
func parseRouteAttribute(attr *sitter.Node, src []byte) (method, path string, ok bool) {
	inner := findChildByType(attr, "attribute")
	if inner == nil {
		return "", "", false
	}
	macroName := ""
	for i := 0; i < int(inner.ChildCount()); i++ {
		child := inner.Child(i)
		if child.Type() == "identifier" {
			macroName = strings.ToLower(child.Content(src))
			break
		}
	}
	verb, found := httpRouteMacros[macroName]
	if !found {
		return "", "", false
	}
	tokens := findChildByType(inner, "token_tree")
	if tokens == nil {
		return "", "", false
	}
	pathStr := firstStringLiteral(tokens, src)
	if pathStr == "" {
		return "", "", false
	}
	return verb, pathStr, true
}

// findChildByType returns the first direct child with the given type, or nil.
func findChildByType(node *sitter.Node, typ string) *sitter.Node {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == typ {
			return child
		}
	}
	return nil
}

// firstStringLiteral walks the subtree and returns the first string literal's
// content with the quotes stripped.
func firstStringLiteral(node *sitter.Node, src []byte) string {
	var found string
	common.WalkTree(node, func(n *sitter.Node) {
		if found != "" {
			return
		}
		if n.Type() != "string_literal" {
			return
		}
		raw := n.Content(src)
		found = strings.Trim(raw, "\"")
	})
	return found
}

// httpClientObjects are the leading identifiers that hint at an outbound HTTP
// call. The grammar lets us match `reqwest::get("...")`, `Client::new()`, etc.
var httpClientObjects = map[string]bool{
	"reqwest": true, "hyper": true, "ureq": true, "isahc": true,
}

// extractHTTPCalls detects outbound HTTP client calls of the shape
// `reqwest::get("https://...")` (function path with a string-literal URL).
func extractHTTPCalls(root *sitter.Node, src []byte, modID string) ([]*model.Node, []*model.Edge) {
	var nodes []*model.Node
	var edges []*model.Edge

	common.WalkTree(root, func(node *sitter.Node) {
		host, method, rawURL, ok := matchHTTPClientCall(node, src)
		if !ok {
			return
		}
		serviceID := "service:" + host
		nodes = append(nodes, &model.Node{
			ID:   serviceID,
			Name: host,
			Type: model.NodeExternalAPI,
			Properties: map[string]string{
				"detected_via": "http_call",
			},
		})
		edges = append(edges, &model.Edge{
			Source:     modID,
			Target:     serviceID,
			Type:       model.EdgeAPICall,
			Label:      method + " " + rawURL,
			Confidence: 0.7,
			Properties: map[string]string{
				"url":  rawURL,
				"line": fmt.Sprintf("%d", int(node.StartPoint().Row)+1),
			},
		})
	})

	return nodes, edges
}

// httpClientCallSyntax holds the parsed shape of `<crate>::<verb>(<args>)`
// where crate is a known HTTP client crate and verb is an HTTP method name.
type httpClientCallSyntax struct {
	args *sitter.Node
	verb string
}

// parseHTTPClientCallSyntax recognizes a call_expression as an HTTP client
// call and returns the verb plus arguments node, or ok=false.
func parseHTTPClientCallSyntax(node *sitter.Node, src []byte) (httpClientCallSyntax, bool) {
	if node.Type() != "call_expression" {
		return httpClientCallSyntax{}, false
	}
	fn := node.ChildByFieldName("function")
	if fn == nil || fn.Type() != "scoped_identifier" {
		return httpClientCallSyntax{}, false
	}
	parts := strings.Split(fn.Content(src), "::")
	if len(parts) < 2 {
		return httpClientCallSyntax{}, false
	}
	verb := strings.ToLower(parts[len(parts)-1])
	if !httpClientObjects[parts[0]] || !isHTTPVerb(verb) {
		return httpClientCallSyntax{}, false
	}
	args := node.ChildByFieldName("arguments")
	if args == nil {
		return httpClientCallSyntax{}, false
	}
	return httpClientCallSyntax{args: args, verb: verb}, true
}

// matchHTTPClientCall recognizes `<crate>::<method>(<url-literal>)` shaped
// call_expression nodes and returns (host, method, rawURL) on a match.
func matchHTTPClientCall(node *sitter.Node, src []byte) (host, method, rawURL string, ok bool) {
	syntax, syntaxOK := parseHTTPClientCallSyntax(node, src)
	if !syntaxOK {
		return "", "", "", false
	}
	rawURL = firstStringLiteral(syntax.args, src)
	if rawURL == "" {
		return "", "", "", false
	}
	h, hostOK := common.ParseServiceFromURL(rawURL)
	if !hostOK {
		return "", "", "", false
	}
	return h, strings.ToUpper(syntax.verb), rawURL, true
}

// isHTTPVerb reports whether v is a recognized HTTP method name (lower case).
func isHTTPVerb(v string) bool {
	_, ok := httpRouteMacros[v]
	return ok
}

// moduleSkippedForSize builds a module node for a Rust file past the size cap.
func moduleSkippedForSize(path string, size int64) *model.Node {
	dir := filepath.Base(filepath.Dir(path))
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return &model.Node{
		ID:       fmt.Sprintf("mod:%s/%s", dir, base),
		Name:     base,
		Type:     model.NodeModule,
		Language: "rust",
		Path:     filepath.Dir(path),
		Properties: map[string]string{
			"skipped":    "size",
			"size_bytes": fmt.Sprintf("%d", size),
		},
	}
}
