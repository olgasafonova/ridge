// Package java provides Java analysis using tree-sitter.
// Detects imports, HTTP endpoints (Spring, JAX-RS, Quarkus), database/queue/cache
// infrastructure, and outbound HTTP client calls.
package java

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/java"

	"github.com/olgasafonova/ridge/internal/analyzer/common"
	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/scanner"
)

// Analyzer implements the scanner.Analyzer interface for Java files.
type Analyzer struct {
	parser *sitter.Parser
}

// New creates a Java analyzer with a tree-sitter parser.
func New() *Analyzer {
	p := sitter.NewParser()
	p.SetLanguage(java.GetLanguage())
	return &Analyzer{parser: p}
}

// Extensions returns Java file extensions.
func (a *Analyzer) Extensions() []string {
	return []string{".java"}
}

// Language returns "java".
func (a *Analyzer) Language() string {
	return "java"
}

// Clone returns an independent copy with a fresh tree-sitter parser.
// Tree-sitter parsers are not thread-safe; each goroutine needs its own.
func (a *Analyzer) Clone() scanner.Analyzer {
	return New()
}

// javaAnalyzerVersion is the cache-invalidation key for this analyzer. Bump it
// when emitted Nodes/Edges change shape or when detector logic changes.
const javaAnalyzerVersion = "java-v1"

// Signature returns the analyzer's behavior version used for cache invalidation.
func (a *Analyzer) Signature() string {
	return javaAnalyzerVersion
}

// maxFileBytes caps source size before parsing. Java enterprise files can be
// large; the 5 MB ceiling matches the other tree-sitter analyzers.
const maxFileBytes = 5 * 1024 * 1024

// Analyze parses a Java file and extracts architectural elements.
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
		Language: "java",
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

// extractImports walks `import com.foo.Bar;` declarations.
// The trailing class name is stripped so `com.foo.Bar` → `com.foo`.
func extractImports(root *sitter.Node, src []byte, modID string) ([]*model.Node, []*model.Edge) {
	var nodes []*model.Node
	var edges []*model.Edge

	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() != "import_declaration" {
			continue
		}
		importPath := extractImportPath(child, src)
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

// extractImportPath reads an import_declaration and returns the package path,
// minus the trailing class identifier. `import com.foo.bar.Baz;` → `com.foo.bar`.
// Wildcard imports `import com.foo.*;` → `com.foo`.
func extractImportPath(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "scoped_identifier":
			return dropLastSegment(child.Content(src))
		case "identifier":
			return child.Content(src)
		}
	}
	return ""
}

// dropLastSegment removes the trailing `.Segment` from a dotted path.
// "com.foo.Bar" → "com.foo"; "java" → "java".
func dropLastSegment(path string) string {
	path = strings.TrimSpace(path)
	if idx := strings.LastIndex(path, "."); idx != -1 {
		return path[:idx]
	}
	return path
}

// infraPatterns defines Java package prefixes for infrastructure detection.
var infraPatterns = []common.InfraPattern{
	{
		Packages: []string{
			"javax.persistence", "jakarta.persistence", "org.hibernate",
			"org.springframework.jdbc", "org.springframework.data",
			"com.mongodb", "org.mongodb", "com.mysql", "org.postgresql",
			"org.mybatis", "com.zaxxer.hikari", "java.sql",
		},
		NodeType: model.NodeDatabase,
		EdgeType: model.EdgeReadWrite,
		NodeID:   "infra:database",
		NodeName: "Database",
	},
	{
		Packages: []string{
			"org.springframework.amqp", "com.rabbitmq", "org.apache.kafka",
			"javax.jms", "jakarta.jms", "org.springframework.jms",
			"io.nats", "com.amazonaws.services.sqs",
		},
		NodeType: model.NodeQueue,
		EdgeType: model.EdgePublish,
		NodeID:   "infra:queue",
		NodeName: "Message Queue",
	},
	{
		Packages: []string{
			"org.springframework.cache", "com.github.benmanes.caffeine",
			"redis.clients", "net.spy.memcached", "org.ehcache",
		},
		NodeType: model.NodeCache,
		EdgeType: model.EdgeReadWrite,
		NodeID:   "infra:cache",
		NodeName: "Cache",
	},
	{
		Packages: []string{
			"org.springframework.web.client", "org.springframework.web.reactive.function.client",
			"java.net.http", "okhttp3", "org.apache.http", "org.apache.hc",
			"feign", "retrofit2",
		},
		NodeType: model.NodeExternalAPI,
		EdgeType: model.EdgeAPICall,
		NodeID:   "infra:external_api",
		NodeName: "External API",
	},
}

// routeAnnotations maps annotation names to the HTTP method they imply.
// Spring's @RequestMapping uses an explicit `method=` value, so it's resolved
// separately in parseRouteAnnotation; here the mapping covers the verb-named
// annotations and JAX-RS / Quarkus path annotations.
var routeAnnotations = map[string]string{
	"GetMapping":     "GET",
	"PostMapping":    "POST",
	"PutMapping":     "PUT",
	"DeleteMapping":  "DELETE",
	"PatchMapping":   "PATCH",
	"RequestMapping": "",     // method derived from arguments or defaults to ANY
	"Path":           "PATH", // JAX-RS / Quarkus class-or-method level path
	"GET":            "GET",
	"POST":           "POST",
	"PUT":            "PUT",
	"DELETE":         "DELETE",
	"PATCH":          "PATCH",
}

// detectFramework picks the HTTP framework by import prefix.
func detectFramework(root *sitter.Node, src []byte) string {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() != "import_declaration" {
			continue
		}
		p := extractImportPath(child, src)
		switch {
		case strings.HasPrefix(p, "org.springframework"):
			return "spring"
		case strings.HasPrefix(p, "io.quarkus"):
			return "quarkus"
		case strings.HasPrefix(p, "jakarta.ws.rs"), strings.HasPrefix(p, "javax.ws.rs"):
			return "jax-rs"
		case strings.HasPrefix(p, "io.micronaut"):
			return "micronaut"
		}
	}
	return "unknown"
}

// routeContext bundles the per-file inputs route extraction needs so the
// function signature stays below the Go arg threshold and the inner callback
// reads as a single dispatch.
type routeContext struct {
	src       []byte
	modID     string
	filePath  string
	framework string
}

// extractRoutes finds @GetMapping / @PostMapping / @RequestMapping annotations
// on methods and emits an endpoint per route. RequestMapping with a method
// value resolves to that verb; otherwise the route is recorded with ANY.
func extractRoutes(root *sitter.Node, rc routeContext) ([]*model.Node, []*model.Edge) {
	var nodes []*model.Node
	var edges []*model.Edge

	common.WalkTree(root, func(node *sitter.Node) {
		if !isAnnotationNode(node) {
			return
		}
		method, path, ok := parseRouteAnnotation(node, rc.src)
		if !ok {
			return
		}
		line := int(node.StartPoint().Row) + 1
		endpointID := fmt.Sprintf("endpoint:%s:%d", filepath.Base(rc.filePath), line)
		nodes = append(nodes, &model.Node{
			ID:       endpointID,
			Name:     fmt.Sprintf("%s %s", method, path),
			Type:     model.NodeEndpoint,
			Language: "java",
			Path:     rc.filePath,
			Properties: map[string]string{
				"method":    method,
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

// isAnnotationNode reports whether n is one of tree-sitter-java's two
// annotation node types: `annotation` (with arguments) or `marker_annotation`
// (no arguments).
func isAnnotationNode(n *sitter.Node) bool {
	t := n.Type()
	return t == "annotation" || t == "marker_annotation"
}

// parseRouteAnnotation returns (method, path, ok) for a route-defining
// annotation. Falls back to ANY when method can't be resolved. Returns
// ok=false for annotations unrelated to routing.
func parseRouteAnnotation(node *sitter.Node, src []byte) (method, path string, ok bool) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return "", "", false
	}
	annName := strings.TrimPrefix(nameNode.Content(src), "@")
	verb, found := routeAnnotations[annName]
	if !found {
		return "", "", false
	}
	if annName == "Path" {
		// JAX-RS / Quarkus @Path is structural; method comes from a sibling
		// HTTP-verb annotation (covered as its own match). Skip here so we
		// don't emit a pseudo-endpoint with method=PATH.
		return "", "", false
	}
	pathStr := firstStringLiteral(node, src)
	if pathStr == "" {
		// @GetMapping with no path arg targets the class-level @RequestMapping;
		// treat it as the root for now.
		pathStr = "/"
	}
	if verb == "" {
		verb = requestMappingMethod(node, src)
	}
	return verb, pathStr, true
}

// requestMappingMethod inspects @RequestMapping arguments for `method=RequestMethod.GET`.
// Returns "ANY" when no method= clause is present.
func requestMappingMethod(node *sitter.Node, src []byte) string {
	args := node.ChildByFieldName("arguments")
	if args == nil {
		return "ANY"
	}
	_, rest, found := strings.Cut(args.Content(src), "RequestMethod.")
	if !found {
		return "ANY"
	}
	end := strings.IndexAny(rest, ", )}")
	if end == -1 {
		end = len(rest)
	}
	verb := strings.TrimSpace(rest[:end])
	if verb == "" {
		return "ANY"
	}
	return strings.ToUpper(verb)
}

// firstStringLiteral walks the annotation subtree for the first string literal.
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

// httpClientObjects are the receiver identifiers that indicate an outbound
// HTTP call. Spring's RestTemplate and WebClient instances are typically
// camel-case variable names — we match the common ones.
var httpClientObjects = map[string]bool{
	"restTemplate":      true,
	"webClient":         true,
	"httpClient":        true,
	"client":            true,
	"okHttpClient":      true,
	"asyncRestTemplate": true,
}

// httpClientMethods are the recognized HTTP verb method names invoked on a
// client object.
var httpClientMethods = map[string]string{
	"get":    "GET",
	"post":   "POST",
	"put":    "PUT",
	"delete": "DELETE",
	"patch":  "PATCH",
	// Spring RestTemplate / WebClient methods
	"getForObject": "GET", "getForEntity": "GET",
	"postForObject": "POST", "postForEntity": "POST", "postForLocation": "POST",
	"exchange": "ANY",
}

// extractHTTPCalls detects outbound HTTP client calls of the shape
// `<client>.<verb>("https://...")` and emits service nodes per host.
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

// httpClientCallSyntax holds the parsed shape of `<obj>.<verb>(<args>)`
// where obj is a known HTTP client receiver and verb is an HTTP method name.
type httpClientCallSyntax struct {
	args   *sitter.Node
	method string
}

// parseHTTPClientCallSyntax recognizes a method_invocation as an HTTP client
// call and returns the resolved verb plus the arguments node, or ok=false.
func parseHTTPClientCallSyntax(node *sitter.Node, src []byte) (httpClientCallSyntax, bool) {
	if node.Type() != "method_invocation" {
		return httpClientCallSyntax{}, false
	}
	obj := node.ChildByFieldName("object")
	name := node.ChildByFieldName("name")
	args := node.ChildByFieldName("arguments")
	if obj == nil || name == nil || args == nil {
		return httpClientCallSyntax{}, false
	}
	if !httpClientObjects[obj.Content(src)] {
		return httpClientCallSyntax{}, false
	}
	verb, knownVerb := httpClientMethods[name.Content(src)]
	if !knownVerb {
		return httpClientCallSyntax{}, false
	}
	return httpClientCallSyntax{args: args, method: verb}, true
}

// matchHTTPClientCall recognizes a method_invocation node of the shape
// `<client>.<verb>(<url>, ...)`. Returns (host, method, rawURL, true) when the
// receiver looks like an HTTP client and the first arg is a URL literal.
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
	return h, syntax.method, rawURL, true
}

// moduleSkippedForSize builds a module node for a Java file past the size cap.
func moduleSkippedForSize(path string, size int64) *model.Node {
	dir := filepath.Base(filepath.Dir(path))
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return &model.Node{
		ID:       fmt.Sprintf("mod:%s/%s", dir, base),
		Name:     base,
		Type:     model.NodeModule,
		Language: "java",
		Path:     filepath.Dir(path),
		Properties: map[string]string{
			"skipped":    "size",
			"size_bytes": fmt.Sprintf("%d", size),
		},
	}
}
