// Package python provides Python analysis using tree-sitter.
// Detects imports, HTTP endpoints (Flask, FastAPI, Django), database connections, and messaging patterns.
package python

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"

	"github.com/olgasafonova/ridge/internal/analyzer/common"
	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/scanner"
)

// Analyzer implements the scanner.Analyzer interface for Python files.
type Analyzer struct {
	parser *sitter.Parser
}

// New creates a Python analyzer with a tree-sitter parser.
func New() *Analyzer {
	p := sitter.NewParser()
	p.SetLanguage(python.GetLanguage())
	return &Analyzer{parser: p}
}

// Extensions returns Python file extensions.
func (a *Analyzer) Extensions() []string {
	return []string{".py"}
}

// Language returns "python".
func (a *Analyzer) Language() string {
	return "python"
}

// Clone returns an independent copy with a fresh tree-sitter parser.
// Tree-sitter parsers are not thread-safe; each goroutine needs its own.
func (a *Analyzer) Clone() scanner.Analyzer {
	return New()
}

// maxFileBytes caps source size before parsing. Files past the cap return a
// module stub with skipped=size, matching the markdown analyzer convention.
// Bounds memory and parser work even with WalkTree's iterative walk.
const maxFileBytes = 5 * 1024 * 1024 // 5 MB

// Analyze parses a Python file and extracts architectural elements.
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

	var nodes []*model.Node
	var edges []*model.Edge

	nodes = append(nodes, &model.Node{
		ID:       modID,
		Name:     base,
		Type:     model.NodeModule,
		Language: "python",
		Path:     filepath.Dir(path),
	})

	// Extract imports
	importNodes, importEdges := extractImports(root, src, modID)
	nodes = append(nodes, importNodes...)
	edges = append(edges, importEdges...)

	// Detect framework from imports before extracting routes
	framework := detectFramework(root, src)

	// Extract route handlers (Flask/FastAPI decorators)
	routeNodes, routeEdges := extractRoutes(root, src, modID, path, framework)
	nodes = append(nodes, routeNodes...)
	edges = append(edges, routeEdges...)

	// Extract Django URL patterns (urls.py files with path()/re_path() calls)
	if strings.HasSuffix(filepath.Base(path), "urls.py") {
		djangoNodes, djangoEdges := extractDjangoURLPatterns(root, src, modID, path)
		nodes = append(nodes, djangoNodes...)
		edges = append(edges, djangoEdges...)
	}

	// Detect outbound HTTP client calls
	callNodes, callEdges := extractHTTPCalls(root, src, modID)
	nodes = append(nodes, callNodes...)
	edges = append(edges, callEdges...)

	return nodes, edges, nil
}

// extractImports walks the root's children for import and from-import statements.
func extractImports(root *sitter.Node, src []byte, modID string) ([]*model.Node, []*model.Edge) {
	var nodes []*model.Node
	var edges []*model.Edge

	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		var importPath string

		switch child.Type() {
		case "import_statement":
			// import os, import sqlalchemy
			importPath = extractDottedName(child, src)
		case "import_from_statement":
			// from flask import Flask
			importPath = extractFromModule(child, src)
		default:
			continue
		}

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

		infraNodes, infraEdges := common.ClassifyImport(importPath, modID, infraPatterns, ".")
		nodes = append(nodes, infraNodes...)
		edges = append(edges, infraEdges...)
	}

	return nodes, edges
}

// extractDottedName gets the module name from an import_statement.
func extractDottedName(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "dotted_name" {
			return child.Content(src)
		}
	}
	return ""
}

// extractFromModule gets the module name from an import_from_statement.
func extractFromModule(node *sitter.Node, src []byte) string {
	moduleName := node.ChildByFieldName("module_name")
	if moduleName != nil {
		return moduleName.Content(src)
	}
	// Fallback: find the dotted_name or relative_import child
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "dotted_name" {
			return child.Content(src)
		}
	}
	return ""
}

// infraPatterns defines Python infrastructure package patterns.
var infraPatterns = []common.InfraPattern{
	{
		Packages: []string{"sqlalchemy", "django.db", "pymongo", "psycopg2", "mysql.connector", "sqlite3", "tortoise", "peewee", "databases", "asyncpg"},
		NodeType: model.NodeDatabase,
		EdgeType: model.EdgeReadWrite,
		NodeID:   "infra:database",
		NodeName: "Database",
	},
	{
		Packages: []string{"celery", "kombu", "pika", "kafka", "rq", "dramatiq"},
		NodeType: model.NodeQueue,
		EdgeType: model.EdgePublish,
		NodeID:   "infra:queue",
		NodeName: "Message Queue",
	},
	{
		Packages: []string{"redis", "pymemcache", "aiocache", "django.core.cache", "cachetools"},
		NodeType: model.NodeCache,
		EdgeType: model.EdgeReadWrite,
		NodeID:   "infra:cache",
		NodeName: "Cache",
	},
	{
		Packages: []string{"requests", "httpx", "aiohttp", "urllib3", "httplib2"},
		NodeType: model.NodeExternalAPI,
		EdgeType: model.EdgeAPICall,
		NodeID:   "infra:external_api",
		NodeName: "External API",
	},
}

// HTTP methods recognized as route decorators.
var httpMethods = map[string]bool{
	"route": true, "get": true, "post": true, "put": true, "delete": true, "patch": true,
	"options": true, "head": true,
}

// detectFramework checks imports to determine the web framework in use.
func detectFramework(root *sitter.Node, src []byte) string {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		var importPath string
		switch child.Type() {
		case "import_statement":
			importPath = extractDottedName(child, src)
		case "import_from_statement":
			importPath = extractFromModule(child, src)
		default:
			continue
		}
		if strings.HasPrefix(importPath, "fastapi") {
			return "fastapi"
		}
		if strings.HasPrefix(importPath, "flask") {
			return "flask"
		}
	}
	return "unknown"
}

// routeContext bundles the per-file inputs that route extraction needs so
// extractRoutes can stay below the function-arg threshold and the inner
// callback can read as a single delegation.
type routeContext struct {
	src       []byte
	modID     string
	filePath  string
	framework string
}

// extractRoutes finds Flask/FastAPI route decorators.
func extractRoutes(root *sitter.Node, src []byte, modID, filePath, framework string) ([]*model.Node, []*model.Edge) {
	rc := routeContext{src: src, modID: modID, filePath: filePath, framework: framework}
	var nodes []*model.Node
	var edges []*model.Edge

	common.WalkTree(root, func(node *sitter.Node) {
		if node.Type() != "decorated_definition" {
			return
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child.Type() != "decorator" {
				continue
			}
			if n, e, ok := buildRouteFromDecorator(child, node, rc); ok {
				nodes = append(nodes, n)
				edges = append(edges, e)
			}
		}
	})

	return nodes, edges
}

// buildRouteFromDecorator parses one decorator and returns the endpoint node,
// "serves" edge, and ok=true when the decorator carries an HTTP route.
func buildRouteFromDecorator(decorator, defNode *sitter.Node, rc routeContext) (*model.Node, *model.Edge, bool) {
	method, routePath := extractDecoratorRoute(decorator, rc.src)
	if routePath == "" {
		return nil, nil, false
	}
	line := int(defNode.StartPoint().Row) + 1
	endpointID := fmt.Sprintf("endpoint:%s:%d", filepath.Base(rc.filePath), line)
	fw := resolveFramework(rc.framework, method)

	node := &model.Node{
		ID:       endpointID,
		Name:     fmt.Sprintf("%s %s", strings.ToUpper(method), routePath),
		Type:     model.NodeEndpoint,
		Language: "python",
		Path:     rc.filePath,
		Properties: map[string]string{
			"method":    method,
			"route":     routePath,
			"line":      fmt.Sprintf("%d", line),
			"framework": fw,
		},
	}
	edge := &model.Edge{
		Source:     rc.modID,
		Target:     endpointID,
		Type:       model.EdgeAPICall,
		Label:      "serves",
		Confidence: 0.85,
	}
	return node, edge, true
}

// resolveFramework picks a concrete framework label when import detection
// returned "unknown": "route" is Flask-typical, anything else maps to FastAPI.
func resolveFramework(detected, method string) string {
	if detected != "unknown" {
		return detected
	}
	if method == "route" {
		return "flask"
	}
	return "fastapi"
}

// extractDecoratorRoute parses a decorator like @app.route('/path') or @app.get('/path').
func extractDecoratorRoute(decorator *sitter.Node, src []byte) (method, routePath string) {
	common.WalkTree(decorator, func(node *sitter.Node) {
		if routePath != "" {
			return // already found
		}
		m, p, ok := parseRouteCall(node, src)
		if !ok {
			return
		}
		method, routePath = m, p
	})
	return method, routePath
}

// parseRouteCall recognizes a tree-sitter call node as @app.<httpmethod>('/path')
// and returns (method, path, true) on success.
func parseRouteCall(node *sitter.Node, src []byte) (string, string, bool) {
	if node.Type() != "call" {
		return "", "", false
	}
	fn := node.ChildByFieldName("function")
	if fn == nil || fn.Type() != "attribute" {
		return "", "", false
	}
	attr := fn.ChildByFieldName("attribute")
	if attr == nil {
		return "", "", false
	}
	methodName := strings.ToLower(attr.Content(src))
	if !httpMethods[methodName] {
		return "", "", false
	}
	args := node.ChildByFieldName("arguments")
	if args == nil {
		return "", "", false
	}
	path := extractFirstStringArg(args, src)
	if path == "" {
		return "", "", false
	}
	return methodName, path, true
}

// extractFirstStringArg finds the first string literal in an argument_list node.
func extractFirstStringArg(args *sitter.Node, src []byte) string {
	for i := 0; i < int(args.ChildCount()); i++ {
		child := args.Child(i)
		if child.Type() == "string" {
			return stripPythonString(child.Content(src))
		}
	}
	return ""
}

// djangoURLFuncs are Django URL routing functions.
var djangoURLFuncs = map[string]bool{
	"path":    true,
	"re_path": true,
}

// djangoURLCall describes one matched Django path()/re_path() invocation.
type djangoURLCall struct {
	funcName string
	route    string
	line     int
}

// matchDjangoURLCall recognizes a tree-sitter call node as path('/x', ...) or
// re_path(r'^x$', ...) and returns the parsed call or ok=false.
func matchDjangoURLCall(node *sitter.Node, src []byte) (djangoURLCall, bool) {
	if node.Type() != "call" {
		return djangoURLCall{}, false
	}
	fn := node.ChildByFieldName("function")
	if fn == nil {
		return djangoURLCall{}, false
	}
	funcName := djangoFuncName(fn, src)
	if !djangoURLFuncs[funcName] {
		return djangoURLCall{}, false
	}
	args := node.ChildByFieldName("arguments")
	if args == nil {
		return djangoURLCall{}, false
	}
	route := extractFirstStringArg(args, src)
	if route == "" {
		return djangoURLCall{}, false
	}
	return djangoURLCall{
		funcName: funcName,
		route:    route,
		line:     int(node.StartPoint().Row) + 1,
	}, true
}

// djangoFuncName returns the bare callee name for an identifier-form call
// (path) or the right-hand side of an attribute-form call (urls.path).
func djangoFuncName(fn *sitter.Node, src []byte) string {
	switch fn.Type() {
	case "identifier":
		return fn.Content(src)
	case "attribute":
		if attr := fn.ChildByFieldName("attribute"); attr != nil {
			return attr.Content(src)
		}
	}
	return ""
}

// extractDjangoURLPatterns finds Django path()/re_path() calls in urls.py files.
func extractDjangoURLPatterns(root *sitter.Node, src []byte, modID, filePath string) ([]*model.Node, []*model.Edge) {
	var nodes []*model.Node
	var edges []*model.Edge

	common.WalkTree(root, func(node *sitter.Node) {
		call, ok := matchDjangoURLCall(node, src)
		if !ok {
			return
		}
		endpointID := fmt.Sprintf("endpoint:%s:%d", filepath.Base(filePath), call.line)
		nodes = append(nodes, &model.Node{
			ID:       endpointID,
			Name:     fmt.Sprintf("URL %s", call.route),
			Type:     model.NodeEndpoint,
			Language: "python",
			Path:     filePath,
			Properties: map[string]string{
				"method":    call.funcName,
				"route":     call.route,
				"line":      fmt.Sprintf("%d", call.line),
				"framework": "django",
			},
		})
		edges = append(edges, &model.Edge{
			Source:     modID,
			Target:     endpointID,
			Type:       model.EdgeAPICall,
			Label:      "serves",
			Confidence: 0.85,
		})
	})

	return nodes, edges
}

// httpClientObjects are receiver names that indicate HTTP client calls in Python.
var httpClientObjects = map[string]bool{
	"requests": true, "httpx": true, "session": true, "client": true,
}

// httpCall describes one matched outbound HTTP client call. Empty host means
// the call did not match the recognized client/method/URL pattern.
type httpCall struct {
	host   string
	method string
	rawURL string
	line   int
}

// matchHTTPClientCall recognizes a tree-sitter call node as an outbound HTTP
// client invocation (requests.get, httpx.post, …) and returns the parsed call
// or ok=false. Pulled out of extractHTTPCalls so the WalkTree callback reads
// as a single dispatch.
func matchHTTPClientCall(node *sitter.Node, src []byte) (httpCall, bool) {
	if node.Type() != "call" {
		return httpCall{}, false
	}
	fn := node.ChildByFieldName("function")
	if fn == nil || fn.Type() != "attribute" {
		return httpCall{}, false
	}
	obj := fn.ChildByFieldName("object")
	attr := fn.ChildByFieldName("attribute")
	if obj == nil || attr == nil {
		return httpCall{}, false
	}
	objName := obj.Content(src)
	methodName := strings.ToLower(attr.Content(src))
	if !httpClientObjects[objName] || !httpMethods[methodName] {
		return httpCall{}, false
	}
	args := node.ChildByFieldName("arguments")
	if args == nil {
		return httpCall{}, false
	}
	rawURL := extractFirstStringArg(args, src)
	if rawURL == "" {
		return httpCall{}, false
	}
	host, ok := common.ParseServiceFromURL(rawURL)
	if !ok {
		return httpCall{}, false
	}
	return httpCall{
		host:   host,
		method: methodName,
		rawURL: rawURL,
		line:   int(node.StartPoint().Row) + 1,
	}, true
}

// extractHTTPCalls detects outbound HTTP calls (requests.get, httpx.post, etc.)
// and creates service nodes for target hosts.
func extractHTTPCalls(root *sitter.Node, src []byte, modID string) ([]*model.Node, []*model.Edge) {
	var nodes []*model.Node
	var edges []*model.Edge

	common.WalkTree(root, func(node *sitter.Node) {
		call, ok := matchHTTPClientCall(node, src)
		if !ok {
			return
		}
		serviceID := "service:" + call.host
		nodes = append(nodes, &model.Node{
			ID:   serviceID,
			Name: call.host,
			Type: model.NodeExternalAPI,
			Properties: map[string]string{
				"detected_via": "http_call",
			},
		})
		edges = append(edges, &model.Edge{
			Source:     modID,
			Target:     serviceID,
			Type:       model.EdgeAPICall,
			Label:      strings.ToUpper(call.method) + " " + call.rawURL,
			Confidence: 0.7,
			Properties: map[string]string{
				"url":  call.rawURL,
				"line": fmt.Sprintf("%d", call.line),
			},
		})
	})

	return nodes, edges
}

// moduleSkippedForSize builds a module node for a Python file past the size
// cap. The module is visible in the graph; no parsing or extraction runs.
func moduleSkippedForSize(path string, size int64) *model.Node {
	dir := filepath.Base(filepath.Dir(path))
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return &model.Node{
		ID:       fmt.Sprintf("mod:%s/%s", dir, base),
		Name:     base,
		Type:     model.NodeModule,
		Language: "python",
		Path:     filepath.Dir(path),
		Properties: map[string]string{
			"skipped":      "size",
			"size_bytes":   fmt.Sprintf("%d", size),
			"max_capacity": fmt.Sprintf("%d", maxFileBytes),
		},
	}
}

// stripPythonString removes quotes from Python string literals.
func stripPythonString(s string) string {
	// Handle triple-quoted strings
	for _, q := range []string{`"""`, `'''`} {
		if strings.HasPrefix(s, q) && strings.HasSuffix(s, q) {
			return s[3 : len(s)-3]
		}
	}
	// Handle single/double quoted strings
	for _, q := range []string{`"`, `'`} {
		if strings.HasPrefix(s, q) && strings.HasSuffix(s, q) {
			return s[1 : len(s)-1]
		}
	}
	return s
}
