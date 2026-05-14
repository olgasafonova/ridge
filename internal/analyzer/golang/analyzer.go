// Package golang provides Go-specific static analysis using go/ast.
// Detects imports, HTTP endpoints, database connections, and messaging patterns.
package golang

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/olgasafonova/ridge/internal/analyzer/common"
	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/scanner"
)

// Analyzer implements the scanner.Analyzer interface for Go source files.
type Analyzer struct{}

// New creates a Go analyzer.
func New() *Analyzer {
	return &Analyzer{}
}

// Extensions returns Go file extensions.
func (a *Analyzer) Extensions() []string {
	return []string{".go"}
}

// Language returns "go".
func (a *Analyzer) Language() string {
	return "go"
}

// Clone returns an independent copy of this analyzer.
// The Go analyzer is stateless, so Clone just creates a new instance.
func (a *Analyzer) Clone() scanner.Analyzer {
	return New()
}

// goAnalyzerVersion is the cache-invalidation key for this analyzer. Bump it
// when emitted Nodes/Edges change shape or when detector logic changes
// (added/removed framework, new infra classification, schema migrations).
const goAnalyzerVersion = "go-v1"

// Signature returns the analyzer's behavior version used for cache invalidation.
func (a *Analyzer) Signature() string {
	return goAnalyzerVersion
}

// Analyze parses a Go file and extracts architectural elements.
func (a *Analyzer) Analyze(path string) ([]*model.Node, []*model.Edge, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	pkgName := file.Name.Name
	relPath := filepath.Base(filepath.Dir(path))
	pkgID := fmt.Sprintf("pkg:%s/%s", relPath, pkgName)

	var nodes []*model.Node
	var edges []*model.Edge

	// Always create a package node for the file's package
	nodes = append(nodes, &model.Node{
		ID:       pkgID,
		Name:     pkgName,
		Type:     model.NodePackage,
		Language: "go",
		Path:     filepath.Dir(path),
	})

	// Extract imports as dependency edges
	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		edges = append(edges, &model.Edge{
			Source:     pkgID,
			Target:     "import:" + importPath,
			Type:       model.EdgeDependency,
			Label:      importPath,
			Confidence: 0.9,
		})

		// Detect known infrastructure imports
		nodes, edges = a.classifyImport(importPath, pkgID, nodes, edges)
	}

	// Detect web framework from imports
	framework := detectFramework(file)

	// Walk AST for endpoint, infrastructure, and HTTP client call patterns
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		newNodes, newEdges := a.analyzeCall(call, pkgID, path, fset, framework)
		nodes = append(nodes, newNodes...)
		edges = append(edges, newEdges...)

		newNodes, newEdges = extractHTTPCalls(call, pkgID, fset)
		nodes = append(nodes, newNodes...)
		edges = append(edges, newEdges...)

		return true
	})

	return nodes, edges, nil
}

// classifyImport detects infrastructure dependencies from import paths.
func (a *Analyzer) classifyImport(importPath, pkgID string, nodes []*model.Node, edges []*model.Edge) ([]*model.Node, []*model.Edge) {
	switch {
	// Database drivers and ORMs
	case strings.Contains(importPath, "database/sql"),
		strings.Contains(importPath, "gorm.io"),
		strings.Contains(importPath, "pgx"),
		strings.Contains(importPath, "go-sql-driver"),
		strings.Contains(importPath, "sqlx"):
		dbNode := &model.Node{
			ID:   "infra:database",
			Name: "Database",
			Type: model.NodeDatabase,
			Properties: map[string]string{
				"detected_via": importPath,
			},
		}
		nodes = append(nodes, dbNode)
		edges = append(edges, &model.Edge{
			Source:     pkgID,
			Target:     dbNode.ID,
			Type:       model.EdgeReadWrite,
			Label:      "database access",
			Confidence: 0.8,
		})

	// Message queues
	case strings.Contains(importPath, "amqp"),
		strings.Contains(importPath, "kafka"),
		strings.Contains(importPath, "nats"),
		strings.Contains(importPath, "rabbitmq"):
		queueNode := &model.Node{
			ID:   "infra:queue",
			Name: "Message Queue",
			Type: model.NodeQueue,
			Properties: map[string]string{
				"detected_via": importPath,
			},
		}
		nodes = append(nodes, queueNode)
		edges = append(edges, &model.Edge{
			Source:     pkgID,
			Target:     queueNode.ID,
			Type:       model.EdgePublish,
			Label:      "message queue",
			Confidence: 0.8,
		})

	// Cache
	case strings.Contains(importPath, "redis"),
		strings.Contains(importPath, "memcache"):
		cacheNode := &model.Node{
			ID:   "infra:cache",
			Name: "Cache",
			Type: model.NodeCache,
			Properties: map[string]string{
				"detected_via": importPath,
			},
		}
		nodes = append(nodes, cacheNode)
		edges = append(edges, &model.Edge{
			Source:     pkgID,
			Target:     cacheNode.ID,
			Type:       model.EdgeReadWrite,
			Label:      "cache access",
			Confidence: 0.8,
		})

	// HTTP clients (external API calls)
	case importPath == "net/http" && strings.Contains(pkgID, "client"):
		apiNode := &model.Node{
			ID:   "infra:external_api",
			Name: "External API",
			Type: model.NodeExternalAPI,
		}
		nodes = append(nodes, apiNode)
		edges = append(edges, &model.Edge{
			Source:     pkgID,
			Target:     apiNode.ID,
			Type:       model.EdgeAPICall,
			Label:      "HTTP client",
			Confidence: 0.8,
		})
	}

	return nodes, edges
}

// frameworkImports maps Go import paths to framework labels.
var frameworkImports = map[string]string{
	"github.com/gin-gonic/gin":    "gin",
	"github.com/go-chi/chi":       "chi",
	"github.com/go-chi/chi/v5":    "chi",
	"github.com/labstack/echo":    "echo",
	"github.com/labstack/echo/v4": "echo",
	"github.com/gofiber/fiber":    "fiber",
	"github.com/gofiber/fiber/v2": "fiber",
	"github.com/gorilla/mux":      "gorilla",
	"net/http":                    "stdlib",
}

// detectFramework checks imports to determine the web framework in use.
func detectFramework(file *ast.File) string {
	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		if fw, ok := frameworkImports[importPath]; ok && fw != "stdlib" {
			return fw
		}
	}
	// Check if net/http is imported (stdlib fallback)
	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		if importPath == "net/http" {
			return "stdlib"
		}
	}
	return "unknown"
}

// analyzeCall inspects function call expressions for endpoint patterns.
func (a *Analyzer) analyzeCall(call *ast.CallExpr, pkgID, filePath string, fset *token.FileSet, framework string) ([]*model.Node, []*model.Edge) {
	var nodes []*model.Node
	var edges []*model.Edge

	funcName := callFuncName(call)
	if funcName == "" {
		return nodes, edges
	}

	// Detect HTTP handler registrations
	// "Always HTTP" methods don't need route validation; ambiguous names (Get, Put, etc.)
	// require the first argument to look like a route path (starts with "/").
	alwaysHTTP := map[string]bool{
		"HandleFunc": true, "Handle": true,
		"Group": true, "Route": true,
	}
	ambiguousHTTP := map[string]bool{
		"Get": true, "Post": true, "Put": true, "Delete": true, "Patch": true,
		"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true,
	}

	if (alwaysHTTP[funcName] || ambiguousHTTP[funcName]) && len(call.Args) >= 1 {
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			route := strings.Trim(lit.Value, `"`)
			// For ambiguous methods, only match if the argument looks like a route path
			if ambiguousHTTP[funcName] && !strings.HasPrefix(route, "/") {
				return nodes, edges
			}
			pos := fset.Position(call.Pos())
			endpointID := fmt.Sprintf("endpoint:%s:%d", filepath.Base(filePath), pos.Line)

			nodes = append(nodes, &model.Node{
				ID:       endpointID,
				Name:     fmt.Sprintf("%s %s", strings.ToUpper(funcName), route),
				Type:     model.NodeEndpoint,
				Language: "go",
				Path:     filePath,
				Properties: map[string]string{
					"method":    funcName,
					"route":     route,
					"line":      fmt.Sprintf("%d", pos.Line),
					"framework": framework,
				},
			})
			edges = append(edges, &model.Edge{
				Source:     pkgID,
				Target:     endpointID,
				Type:       model.EdgeAPICall,
				Label:      "serves",
				Confidence: 0.85,
			})
		}
	}

	return nodes, edges
}

// httpClientFuncs maps function names to the argument index containing the URL.
// http.Get(url) → index 0, http.Post(url, contentType, body) → index 0,
// http.NewRequest(method, url, body) → index 1.
var httpClientFuncs = map[string]int{
	"Get":        0,
	"Head":       0,
	"Post":       0,
	"PostForm":   0,
	"NewRequest": 1,
}

// httpClientReceivers are the package/receiver names that indicate HTTP client calls.
var httpClientReceivers = map[string]bool{
	"http": true,
}

// extractHTTPCalls detects outbound HTTP client calls (http.Get, http.Post, etc.)
// and creates service nodes for the target hosts.
func extractHTTPCalls(call *ast.CallExpr, pkgID string, fset *token.FileSet) ([]*model.Node, []*model.Edge) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, nil
	}

	ident, ok := sel.X.(*ast.Ident)
	if !ok || !httpClientReceivers[ident.Name] {
		return nil, nil
	}

	urlArgIdx, known := httpClientFuncs[sel.Sel.Name]
	if !known || len(call.Args) <= urlArgIdx {
		return nil, nil
	}

	lit, ok := call.Args[urlArgIdx].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil, nil
	}

	rawURL := strings.Trim(lit.Value, `"`)
	host, ok := common.ParseServiceFromURL(rawURL)
	if !ok {
		return nil, nil
	}

	serviceID := "service:" + host
	pos := fset.Position(call.Pos())
	method := strings.ToUpper(sel.Sel.Name)
	if method == "NEWREQUEST" && len(call.Args) > 0 {
		if mLit, ok := call.Args[0].(*ast.BasicLit); ok && mLit.Kind == token.STRING {
			method = strings.Trim(mLit.Value, `"`)
		}
	}

	node := &model.Node{
		ID:   serviceID,
		Name: host,
		Type: model.NodeExternalAPI,
		Properties: map[string]string{
			"detected_via": "http_call",
		},
	}
	edge := &model.Edge{
		Source:     pkgID,
		Target:     serviceID,
		Type:       model.EdgeAPICall,
		Label:      method + " " + rawURL,
		Confidence: 0.7,
		Properties: map[string]string{
			"url":  rawURL,
			"line": fmt.Sprintf("%d", pos.Line),
		},
	}
	return []*model.Node{node}, []*model.Edge{edge}
}

// callFuncName extracts the function name from a call expression.
func callFuncName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}
