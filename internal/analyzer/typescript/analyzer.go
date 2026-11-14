// Package typescript provides TypeScript/TSX analysis using tree-sitter.
// Detects imports, HTTP endpoints, database connections, and messaging patterns.
package typescript

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/olgasafonova/ridge/internal/analyzer/common"
	"github.com/olgasafonova/ridge/internal/model"
	"github.com/olgasafonova/ridge/internal/scanner"
)

// Analyzer implements the scanner.Analyzer interface for TypeScript/TSX files.
type Analyzer struct {
	tsParser  *sitter.Parser
	tsxParser *sitter.Parser
	aliases   *aliasCache
}

// New creates a TypeScript analyzer with tree-sitter parsers.
func New() *Analyzer {
	tsParser := sitter.NewParser()
	tsParser.SetLanguage(typescript.GetLanguage())

	tsxParser := sitter.NewParser()
	tsxParser.SetLanguage(tsx.GetLanguage())

	return &Analyzer{
		tsParser:  tsParser,
		tsxParser: tsxParser,
		aliases:   newAliasCache(),
	}
}

// Extensions returns the file extensions this analyzer handles. Svelte
// components are read through their <script> blocks only.
func (a *Analyzer) Extensions() []string {
	return []string{".ts", ".tsx", svelteExt}
}

// Language returns "typescript".
func (a *Analyzer) Language() string {
	return "typescript"
}

// Clone returns an independent copy with fresh tree-sitter parsers.
// Tree-sitter parsers are not thread-safe; each goroutine needs its own.
func (a *Analyzer) Clone() scanner.Analyzer {
	return New()
}

// tsAnalyzerVersion is the cache-invalidation key for this analyzer. Bump it
// when emitted Nodes/Edges change shape or when detector logic changes
// (added/removed framework, new infra classification, schema migrations).
const tsAnalyzerVersion = "ts-v4"

// Signature returns the analyzer's behavior version used for cache invalidation.
func (a *Analyzer) Signature() string {
	return tsAnalyzerVersion
}

// maxFileBytes caps source size before parsing. Files past the cap return a
// module stub with skipped=size, matching the markdown analyzer convention.
// Bounds memory and parser work even with WalkTree's iterative walk.
const maxFileBytes = 5 * 1024 * 1024 // 5 MB

// Analyze parses a TypeScript file with no scan root. Module IDs fall back to
// the parent-folder form and relative imports stay unresolved; the scanner
// uses AnalyzeRooted instead.
func (a *Analyzer) Analyze(path string) ([]*model.Node, []*model.Edge, error) {
	return a.AnalyzeRooted("", path)
}

// AnalyzeRooted parses a TypeScript file and extracts architectural elements.
// With a scan root, the module ID is the root-relative path (unique per file)
// and relative imports are resolved to the module they point at.
func (a *Analyzer) AnalyzeRooted(scanRoot, path string) ([]*model.Node, []*model.Edge, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() > maxFileBytes {
		return []*model.Node{moduleSkippedForSize(scanRoot, path, info.Size())}, nil, nil
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}

	if filepath.Ext(path) == svelteExt {
		src = svelteScriptSource(src)
	}
	tree, err := a.parserFor(path).ParseCtx(context.Background(), nil, src)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	defer tree.Close()
	root := tree.RootNode()

	modID := moduleID(scanRoot, path)

	nodes := []*model.Node{{
		ID:       modID,
		Name:     moduleName(path),
		Type:     model.NodeModule,
		Language: languageOf(path),
		Path:     filepath.Dir(path),
	}}
	var edges []*model.Edge

	importNodes, importEdges := extractImports(root, src, a.importerFor(scanRoot, path, modID))
	nodes = append(nodes, importNodes...)
	edges = append(edges, importEdges...)

	ctx := fileCtx{
		src:      src,
		modID:    modID,
		scanRoot: scanRoot,
		filePath: path,
		// Detect framework from imports before extracting routes
		framework: detectFramework(root, src),
	}

	extractors := []func(*sitter.Node, fileCtx) ([]*model.Node, []*model.Edge){
		extractRoutes,
		extractSvelteKitRoutes,
		extractHTTPCalls,
	}
	if ctx.framework == "nestjs" {
		extractors = append(extractors, extractNestJSRoutes)
	}
	for _, extract := range extractors {
		n, e := extract(root, ctx)
		nodes = append(nodes, n...)
		edges = append(edges, e...)
	}

	return nodes, edges, nil
}

// parserFor picks the grammar for the file at path. Svelte script blocks and
// plain TypeScript share one; only .tsx needs the JSX-aware grammar.
func (a *Analyzer) parserFor(path string) *sitter.Parser {
	if filepath.Ext(path) == ".tsx" {
		return a.tsxParser
	}
	return a.tsParser
}

// languageOf names the source language of the file at path for node metadata.
func languageOf(path string) string {
	if filepath.Ext(path) == svelteExt {
		return "svelte"
	}
	return "typescript"
}

// extractImports walks the top-level import statements and emits dependency
// edges plus any infrastructure nodes/edges classified from each import path.
func extractImports(root *sitter.Node, src []byte, imp importer) ([]*model.Node, []*model.Edge) {
	modID := imp.modID
	var nodes []*model.Node
	var edges []*model.Edge
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() != "import_statement" {
			continue
		}
		importPath := extractImportSource(child, src)
		if importPath == "" {
			continue
		}
		edges = append(edges, &model.Edge{
			Source:     modID,
			Target:     imp.target(importPath),
			Type:       model.EdgeDependency,
			Label:      importPath,
			Confidence: 0.9,
		})
		infraNodes, infraEdges := common.ClassifyImport(importPath, modID, infraPatterns, "/")
		nodes = append(nodes, infraNodes...)
		edges = append(edges, infraEdges...)
	}
	return nodes, edges
}

// extractImportSource gets the import path string from an import_statement node.
func extractImportSource(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "string" || child.Type() == "string_fragment" {
			return stripQuotes(child.Content(src))
		}
	}
	// Try named child "source"
	source := node.ChildByFieldName("source")
	if source != nil {
		return stripQuotes(source.Content(src))
	}
	return ""
}

func stripQuotes(s string) string {
	s = strings.TrimPrefix(s, "'")
	s = strings.TrimSuffix(s, "'")
	s = strings.TrimPrefix(s, "\"")
	s = strings.TrimSuffix(s, "\"")
	return s
}

// infraPatterns defines TypeScript infrastructure package patterns.
var infraPatterns = []common.InfraPattern{
	{
		Packages: []string{"pg", "knex", "prisma", "@prisma/client", "typeorm", "sequelize", "mongoose", "mongodb", "mysql2", "better-sqlite3", "drizzle-orm", "mikro-orm"},
		NodeType: model.NodeDatabase,
		EdgeType: model.EdgeReadWrite,
		NodeID:   "infra:database",
		NodeName: "Database",
	},
	{
		Packages: []string{"amqplib", "kafkajs", "bullmq", "bull", "@google-cloud/pubsub", "nats", "@azure/service-bus"},
		NodeType: model.NodeQueue,
		EdgeType: model.EdgePublish,
		NodeID:   "infra:queue",
		NodeName: "Message Queue",
	},
	{
		Packages: []string{"redis", "ioredis", "@redis/client", "memcached", "keyv"},
		NodeType: model.NodeCache,
		EdgeType: model.EdgeReadWrite,
		NodeID:   "infra:cache",
		NodeName: "Cache",
	},
	{
		Packages: []string{"axios", "node-fetch", "got", "undici", "superagent", "@apollo/client", "graphql-request"},
		NodeType: model.NodeExternalAPI,
		EdgeType: model.EdgeAPICall,
		NodeID:   "infra:external_api",
		NodeName: "External API",
	},
}

// HTTP method names that indicate route registration.
var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "delete": true, "patch": true,
	"options": true, "head": true, "all": true, "use": true, "route": true,
}

// frameworkPackages maps npm package names to framework labels.
var frameworkPackages = map[string]string{
	"express":        "express",
	"fastify":        "fastify",
	"koa":            "koa",
	"hapi":           "hapi",
	"@hapi/hapi":     "hapi",
	"restify":        "restify",
	"@nestjs/common": "nestjs",
	"@nestjs/core":   "nestjs",
}

// nestjsRouteDecorators maps NestJS HTTP method decorator names to HTTP methods.
var nestjsRouteDecorators = map[string]string{
	"Get": "GET", "Post": "POST", "Put": "PUT",
	"Delete": "DELETE", "Patch": "PATCH", "All": "ALL",
	"Head": "HEAD", "Options": "OPTIONS",
}

// detectFramework checks imports to determine the web framework in use.
func detectFramework(root *sitter.Node, src []byte) string {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() != "import_statement" {
			continue
		}
		importPath := extractImportSource(child, src)
		if fw, ok := frameworkPackages[importPath]; ok {
			return fw
		}
	}
	return "unknown"
}

// fileCtx bundles the per-file context an analyzer step needs to emit endpoint
// nodes/edges. Reduces extractRoutes from 5 positional args to 2.
type fileCtx struct {
	src       []byte
	modID     string
	scanRoot  string // absolute scan root; "" when unknown
	filePath  string
	framework string
}

// locatedID builds a node ID for something found at a line of the file:
// "<kind>:<root-relative path>:<line>". Without a scan root it falls back to
// the bare file name, which collides for same-named files in different folders.
func (c fileCtx) locatedID(kind string, line int) string {
	file := filepath.Base(c.filePath)
	if rel, ok := relToRoot(c.scanRoot, c.filePath); ok {
		file = rel
	}
	return fmt.Sprintf("%s:%s:%d", kind, file, line)
}

func extractRoutes(root *sitter.Node, ctx fileCtx) ([]*model.Node, []*model.Edge) {
	var nodes []*model.Node
	var edges []*model.Edge

	common.WalkTree(root, func(node *sitter.Node) {
		method, route, ok := matchRouteCall(node, ctx)
		if !ok {
			return
		}
		line := int(node.StartPoint().Row) + 1
		endpointID := ctx.locatedID("endpoint", line)

		nodes = append(nodes, &model.Node{
			ID:       endpointID,
			Name:     fmt.Sprintf("%s %s", strings.ToUpper(method), route),
			Type:     model.NodeEndpoint,
			Language: "typescript",
			Path:     ctx.filePath,
			Properties: map[string]string{
				"method":    method,
				"route":     route,
				"line":      fmt.Sprintf("%d", line),
				"framework": ctx.framework,
			},
		})
		edges = append(edges, &model.Edge{
			Source:     ctx.modID,
			Target:     endpointID,
			Type:       model.EdgeAPICall,
			Label:      "serves",
			Confidence: 0.85,
		})
	})

	return nodes, edges
}

// routerReceivers are the names a router or app object usually goes by.
var routerReceivers = map[string]bool{
	"app": true, "router": true, "server": true, "fastify": true,
	"route": true, "routes": true, "instance": true,
}

// matchRouteCall recognizes router.get('/path', handler) style calls and
// returns (method, route, true). Anything else returns ok=false.
//
// A method name alone is not enough: form.get('file') and
// headers.get('content-type') have the same shape. See isRouteRegistration.
func matchRouteCall(node *sitter.Node, ctx fileCtx) (method, route string, ok bool) {
	if node.Type() != "call_expression" {
		return "", "", false
	}
	fn := node.ChildByFieldName("function")
	if fn == nil || fn.Type() != "member_expression" {
		return "", "", false
	}
	prop := fn.ChildByFieldName("property")
	if prop == nil {
		return "", "", false
	}
	method = strings.ToLower(prop.Content(ctx.src))
	if !httpMethods[method] {
		return "", "", false
	}
	route, hasArg := firstStringArg(node, ctx.src)
	if !hasArg {
		return "", "", false
	}
	call := routeCall{
		receiver: receiverName(fn.ChildByFieldName("object"), ctx.src),
		route:    route,
		argCount: argCount(node),
		inWebApp: ctx.framework != "unknown",
	}
	if !call.isRouteRegistration() {
		return "", "", false
	}
	return method, route, true
}

// routeCall is what is known about an x.get('...') shaped call.
type routeCall struct {
	receiver string // last name of the object the method is called on
	route    string // first argument
	argCount int
	inWebApp bool // the file imports a web framework
}

// isRouteRegistration separates route registrations from other calls to a
// method named get, post, delete and so on.
//
//   - A route that looks like a path ("/users", "*") counts when the receiver
//     is a known router name, the file imports a web framework, or the call
//     also passes a handler. HTTP client receivers (axios.get('/users')) are
//     outbound calls unless the file imports a web framework.
//   - Any other string ('users') counts only on a known router name with a
//     handler, which leaves out app.get('env') and form.get('file').
func (c routeCall) isRouteRegistration() bool {
	known := routerReceivers[c.receiver]
	hasHandler := c.argCount >= 2
	if !strings.HasPrefix(c.route, "/") && c.route != "*" {
		return known && hasHandler
	}
	if httpClientObjects[c.receiver] && !c.inWebApp {
		return false
	}
	return known || c.inWebApp || hasHandler
}

// receiverName returns the last name in the object a method is called on:
// "router" for router.get, "app" for this.app.get, "" for a call result.
func receiverName(obj *sitter.Node, src []byte) string {
	if obj == nil {
		return ""
	}
	switch obj.Type() {
	case "identifier":
		return obj.Content(src)
	case "member_expression":
		if prop := obj.ChildByFieldName("property"); prop != nil {
			return prop.Content(src)
		}
	}
	return ""
}

// argCount returns the number of arguments passed to a call_expression.
func argCount(call *sitter.Node) int {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return 0
	}
	return int(args.NamedChildCount())
}

// extractNestJSRoutes detects NestJS @Controller/@Get/@Post decorator patterns
// and creates endpoint nodes. Also detects @Injectable() as service nodes.
func extractNestJSRoutes(root *sitter.Node, ctx fileCtx) ([]*model.Node, []*model.Edge) {
	var nodes []*model.Node
	var edges []*model.Edge

	common.WalkTree(root, func(node *sitter.Node) {
		if node.Type() != "class_declaration" {
			return
		}
		ctrlPath := findControllerPath(node, ctx.src)
		if ctrlPath == "" {
			n, e := extractInjectableService(node, ctx)
			nodes = append(nodes, n...)
			edges = append(edges, e...)
			return
		}
		n, e := extractControllerEndpoints(node, ctx, ctrlPath)
		nodes = append(nodes, n...)
		edges = append(edges, e...)
	})

	return nodes, edges
}

// extractInjectableService emits a service node + provides-edge when classNode
// carries an @Injectable() decorator. Returns nil/nil otherwise.
func extractInjectableService(classNode *sitter.Node, ctx fileCtx) ([]*model.Node, []*model.Edge) {
	if !hasDecorator(classNode, ctx.src, "Injectable") {
		return nil, nil
	}
	className := extractClassName(classNode, ctx.src)
	if className == "" {
		return nil, nil
	}
	line := int(classNode.StartPoint().Row) + 1
	serviceID := ctx.locatedID("service", line)
	node := &model.Node{
		ID:       serviceID,
		Name:     className,
		Type:     model.NodeModule,
		Language: "typescript",
		Path:     ctx.filePath,
		Properties: map[string]string{
			"injectable": "true",
			"framework":  ctx.framework,
			"line":       fmt.Sprintf("%d", line),
		},
	}
	edge := &model.Edge{
		Source:     ctx.modID,
		Target:     serviceID,
		Type:       model.EdgeDependency,
		Label:      "provides",
		Confidence: 0.85,
	}
	return []*model.Node{node}, []*model.Edge{edge}
}

// extractControllerEndpoints walks a class body and emits endpoint nodes for
// every @Get/@Post/etc decorator on a method.
func extractControllerEndpoints(classNode *sitter.Node, ctx fileCtx, ctrlPath string) ([]*model.Node, []*model.Edge) {
	body := classNode.ChildByFieldName("body")
	if body == nil {
		return nil, nil
	}
	var nodes []*model.Node
	var edges []*model.Edge
	var pending []*sitter.Node

	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		if child.Type() == "decorator" {
			pending = append(pending, child)
			continue
		}
		if child.Type() != "method_definition" {
			pending = nil
			continue
		}
		method, route := methodRoute(pending, child, ctx.src)
		pending = nil
		if method == "" {
			continue
		}
		n, e := buildNestJSEndpoint(child, ctx, joinPaths(ctrlPath, route), method)
		nodes = append(nodes, n)
		edges = append(edges, e)
	}
	return nodes, edges
}

// methodRoute resolves the HTTP method + route for a method_definition, checking
// both sibling decorators (collected in pending) and the method's own children.
func methodRoute(pending []*sitter.Node, method *sitter.Node, src []byte) (string, string) {
	if httpMethod, routePath := findRouteDecorator(pending, src); httpMethod != "" {
		return httpMethod, routePath
	}
	return findRouteDecorator(collectChildDecorators(method), src)
}

func buildNestJSEndpoint(method *sitter.Node, ctx fileCtx, fullPath, httpMethod string) (*model.Node, *model.Edge) {
	line := int(method.StartPoint().Row) + 1
	endpointID := ctx.locatedID("endpoint", line)
	node := &model.Node{
		ID:       endpointID,
		Name:     fmt.Sprintf("%s %s", httpMethod, fullPath),
		Type:     model.NodeEndpoint,
		Language: "typescript",
		Path:     ctx.filePath,
		Properties: map[string]string{
			"method":    strings.ToLower(httpMethod),
			"route":     fullPath,
			"line":      fmt.Sprintf("%d", line),
			"framework": ctx.framework,
		},
	}
	edge := &model.Edge{
		Source:     ctx.modID,
		Target:     endpointID,
		Type:       model.EdgeAPICall,
		Label:      "serves",
		Confidence: 0.85,
	}
	return node, edge
}

// classDecorators returns the decorators attached to classNode, including
// preceding-sibling decorators in the parent (NestJS classes wrapped in
// export_statement carry their decorators as siblings rather than children).
func classDecorators(classNode *sitter.Node) []*sitter.Node {
	decorators := collectChildDecorators(classNode)
	parent := classNode.Parent()
	if parent == nil {
		return decorators
	}
	for i := 0; i < int(parent.ChildCount()); i++ {
		child := parent.Child(i)
		if child == classNode {
			break
		}
		if child.Type() == "decorator" {
			decorators = append(decorators, child)
		}
	}
	return decorators
}

// findControllerPath looks for @Controller('path') on a class_declaration.
// Returns "/" for @Controller() with no argument, or "" if not a controller.
func findControllerPath(classNode *sitter.Node, src []byte) string {
	for _, d := range classDecorators(classNode) {
		name, arg := extractDecoratorInfo(d, src)
		if name != "Controller" {
			continue
		}
		if arg == "" {
			return "/"
		}
		return arg
	}
	return ""
}

// hasDecorator checks if a class has a specific decorator (by name).
func hasDecorator(classNode *sitter.Node, src []byte, decoratorName string) bool {
	for _, d := range classDecorators(classNode) {
		name, _ := extractDecoratorInfo(d, src)
		if name == decoratorName {
			return true
		}
	}
	return false
}

// extractDecoratorInfo extracts the decorator name and first string argument.
// For @Controller('/users') returns ("Controller", "/users").
// For @Get() returns ("Get", "").
func extractDecoratorInfo(decorator *sitter.Node, src []byte) (name, arg string) {
	for i := 0; i < int(decorator.ChildCount()); i++ {
		child := decorator.Child(i)
		switch child.Type() {
		case "call_expression":
			return extractCallDecorator(child, src)
		case "identifier":
			return child.Content(src), ""
		}
	}
	return "", ""
}

func extractCallDecorator(call *sitter.Node, src []byte) (name, arg string) {
	if fn := call.ChildByFieldName("function"); fn != nil && fn.Type() == "identifier" {
		name = fn.Content(src)
	}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return name, ""
	}
	for j := 0; j < int(args.ChildCount()); j++ {
		argChild := args.Child(j)
		if argChild.Type() == "string" {
			return name, extractStringLiteral(argChild, src)
		}
	}
	return name, ""
}

// extractClassName gets the class name from a class_declaration node.
func extractClassName(node *sitter.Node, src []byte) string {
	nameNode := node.ChildByFieldName("name")
	if nameNode != nil {
		return nameNode.Content(src)
	}
	return ""
}

// findRouteDecorator checks a slice of decorator nodes for NestJS route decorators.
// Returns the HTTP method and route path, or empty strings if none found.
func findRouteDecorator(decorators []*sitter.Node, src []byte) (httpMethod, routePath string) {
	for _, d := range decorators {
		name, arg := extractDecoratorInfo(d, src)
		if method, ok := nestjsRouteDecorators[name]; ok {
			return method, arg
		}
	}
	return "", ""
}

// collectChildDecorators gathers decorator child nodes from a given parent.
func collectChildDecorators(node *sitter.Node) []*sitter.Node {
	var decorators []*sitter.Node
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "decorator" {
			decorators = append(decorators, child)
		}
	}
	return decorators
}

// joinPaths combines a controller base path with a method route path.
func joinPaths(base, route string) string {
	base = strings.TrimSuffix(base, "/")
	if route == "" {
		if base == "" {
			return "/"
		}
		return base
	}
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	return base + route
}

// httpClientObjects are receiver/object names that indicate HTTP client calls.
var httpClientObjects = map[string]bool{
	"axios": true, "http": true, "client": true, "api": true,
}

// extractHTTPCalls detects outbound HTTP calls (fetch, axios.get, etc.). A
// call to an absolute URL creates a service node for the target host. A call
// to a path on the same origin (fetch('/api/analyze')) emits an edge to a
// "route:<METHOD> <path>" placeholder, which the graph resolves to the
// endpoint serving that path.
func extractHTTPCalls(root *sitter.Node, ctx fileCtx) ([]*model.Node, []*model.Edge) {
	var nodes []*model.Node
	var edges []*model.Edge

	common.WalkTree(root, func(node *sitter.Node) {
		if node.Type() != "call_expression" {
			return
		}
		method, ok := httpCallMethod(node, ctx.src)
		if !ok {
			return
		}
		rawURL, ok := firstURLArg(node, ctx.src)
		if !ok {
			return
		}
		edge := &model.Edge{
			Source:     ctx.modID,
			Type:       model.EdgeAPICall,
			Label:      method + " " + rawURL,
			Confidence: 0.7,
			Properties: map[string]string{
				"url":  rawURL,
				"line": fmt.Sprintf("%d", int(node.StartPoint().Row)+1),
			},
		}
		if isSameOriginPath(rawURL) {
			edge.Target = model.RouteTarget(method, rawURL)
			edges = append(edges, edge)
			return
		}
		host, ok := common.ParseServiceFromURL(strings.ReplaceAll(rawURL, model.RouteWildcard, "x"))
		if !ok {
			return
		}
		edge.Target = "service:" + host
		nodes = append(nodes, &model.Node{
			ID:   edge.Target,
			Name: host,
			Type: model.NodeExternalAPI,
			Properties: map[string]string{
				"detected_via": "http_call",
			},
		})
		edges = append(edges, edge)
	})

	return nodes, edges
}

// isSameOriginPath reports whether url is a path on the app's own origin.
func isSameOriginPath(url string) bool {
	return strings.HasPrefix(url, "/") && !strings.HasPrefix(url, "//")
}

// httpCallMethod returns the HTTP method for a call_expression, or ok=false
// if it isn't a recognized HTTP client invocation (fetch / axios.get / etc.).
func httpCallMethod(call *sitter.Node, src []byte) (string, bool) {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return "", false
	}
	switch fn.Type() {
	case "identifier":
		if fn.Content(src) == "fetch" {
			return fetchMethod(call, src), true
		}
		return "", false
	case "member_expression":
		return memberHTTPMethod(fn, src)
	}
	return "", false
}

// fetchMethod reads the method from fetch(url, { method: 'POST' }). GET is
// the default when no options object names one.
func fetchMethod(call *sitter.Node, src []byte) string {
	if m := stringProperty(fetchOptions(call), "method", src); m != "" {
		return strings.ToUpper(m)
	}
	return "GET"
}

// fetchOptions returns the options object literal passed as the second
// argument of a fetch call, or nil.
func fetchOptions(call *sitter.Node) *sitter.Node {
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() < 2 {
		return nil
	}
	if opts := args.NamedChild(1); opts.Type() == "object" {
		return opts
	}
	return nil
}

// stringProperty returns the string literal assigned to key in an object
// literal, or "" when obj is nil or has no such property.
func stringProperty(obj *sitter.Node, key string, src []byte) string {
	if obj == nil {
		return ""
	}
	for i := 0; i < int(obj.NamedChildCount()); i++ {
		pair := obj.NamedChild(i)
		if pair.Type() != "pair" || propertyName(pair.ChildByFieldName("key"), src) != key {
			continue
		}
		if value := pair.ChildByFieldName("value"); value != nil {
			return extractStringLiteral(value, src)
		}
	}
	return ""
}

// propertyName returns an object key without its quotes; "" for a nil node.
func propertyName(key *sitter.Node, src []byte) string {
	if key == nil {
		return ""
	}
	return stripQuotes(key.Content(src))
}

func memberHTTPMethod(fn *sitter.Node, src []byte) (string, bool) {
	obj := fn.ChildByFieldName("object")
	prop := fn.ChildByFieldName("property")
	if obj == nil || prop == nil {
		return "", false
	}
	propName := strings.ToLower(prop.Content(src))
	if !httpClientObjects[obj.Content(src)] || !httpMethods[propName] {
		return "", false
	}
	return strings.ToUpper(propName), true
}

// firstStringArg returns the first string-literal argument to a call_expression.
func firstStringArg(call *sitter.Node, src []byte) (string, bool) {
	firstArg := firstArg(call)
	if firstArg == nil {
		return "", false
	}
	s := extractStringLiteral(firstArg, src)
	return s, s != ""
}

// firstURLArg returns the first argument of an HTTP call as a URL. A template
// literal is accepted too, with each ${...} replaced by model.RouteWildcard.
func firstURLArg(call *sitter.Node, src []byte) (string, bool) {
	arg := firstArg(call)
	if arg == nil {
		return "", false
	}
	if arg.Type() != "template_string" {
		s := extractStringLiteral(arg, src)
		return s, s != ""
	}
	var sb strings.Builder
	for i := 0; i < int(arg.ChildCount()); i++ {
		switch child := arg.Child(i); child.Type() {
		case "template_substitution":
			sb.WriteString(model.RouteWildcard)
		case "`":
		default:
			sb.WriteString(child.Content(src))
		}
	}
	return sb.String(), sb.Len() > 0
}

func firstArg(call *sitter.Node) *sitter.Node {
	args := call.ChildByFieldName("arguments")
	if args == nil || args.ChildCount() < 2 {
		return nil
	}
	return args.Child(1) // Child(0) is "("
}

// extractStringLiteral extracts a string value from a tree-sitter string node.
func extractStringLiteral(node *sitter.Node, src []byte) string {
	if node.Type() == "string" {
		// String node contains quote children + string_fragment
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child.Type() == "string_fragment" {
				return child.Content(src)
			}
		}
		// Fallback: strip quotes from the full content
		return stripQuotes(node.Content(src))
	}
	return ""
}

// moduleSkippedForSize builds a module node for a TypeScript file past the
// size cap. The module is visible in the graph; no parsing or extraction runs.
func moduleSkippedForSize(root, path string, size int64) *model.Node {
	return &model.Node{
		ID:       moduleID(root, path),
		Name:     moduleName(path),
		Type:     model.NodeModule,
		Language: languageOf(path),
		Path:     filepath.Dir(path),
		Properties: map[string]string{
			"skipped":      "size",
			"size_bytes":   fmt.Sprintf("%d", size),
			"max_capacity": fmt.Sprintf("%d", maxFileBytes),
		},
	}
}
