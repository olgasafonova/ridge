package typescript

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/olgasafonova/ridge/internal/model"
)

// SvelteKit declares routes through the file system rather than router calls.
// Under the routes folder (src/routes by default):
//
//   - +server.ts serves its folder's path for every HTTP verb it exports:
//     "export async function POST" in src/routes/api/analyze/+server.ts is
//     POST /api/analyze.
//   - +page.server.ts may export "actions", an object of form actions. Each
//     is a POST to the page's path: the "default" action to the path itself,
//     a named one to "<path>?/<name>".

// svelteKitVerbs are the export names SvelteKit treats as request handlers.
var svelteKitVerbs = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "OPTIONS": true, "HEAD": true,
}

// extractSvelteKitRoutes emits endpoint nodes for a +server.ts or
// +page.server.ts file inside a routes folder.
func extractSvelteKitRoutes(root *sitter.Node, ctx fileCtx) ([]*model.Node, []*model.Edge) {
	base := moduleName(ctx.filePath)
	if base != "+server" && base != "+page.server" {
		return nil, nil
	}
	route, ok := svelteKitRoute(ctx)
	if !ok {
		return nil, nil
	}
	out := &svelteKitEndpoints{ctx: ctx, route: route}
	for _, exp := range exportedNames(root, ctx.src) {
		if base == "+server" {
			out.addVerb(exp)
		} else {
			out.addActions(exp)
		}
	}
	return out.nodes, out.edges
}

// svelteKitEndpoints collects the endpoints one route file serves.
type svelteKitEndpoints struct {
	ctx   fileCtx
	route string
	nodes []*model.Node
	edges []*model.Edge
}

// addVerb adds an endpoint when exp is a request handler export (GET, POST…).
func (s *svelteKitEndpoints) addVerb(exp exported) {
	if svelteKitVerbs[exp.name] {
		s.add(exp.name, exp.name+" "+s.route, exp.line, nil)
	}
}

// addActions adds one POST endpoint per form action when exp is the
// "actions" export of a +page.server.ts.
func (s *svelteKitEndpoints) addActions(exp exported) {
	if exp.name != "actions" {
		return
	}
	for _, action := range objectKeys(exp.value, s.ctx.src) {
		name := "POST " + s.route
		if action.name != "default" {
			name += "?/" + action.name
		}
		s.add("POST", name, action.line, map[string]string{"action": action.name})
	}
}

func (s *svelteKitEndpoints) add(method, name string, line int, extra map[string]string) {
	id := s.ctx.locatedID("endpoint", line)
	props := map[string]string{
		"method":    strings.ToLower(method),
		"route":     s.route,
		"line":      fmt.Sprintf("%d", line),
		"framework": "sveltekit",
	}
	for k, v := range extra {
		props[k] = v
	}
	s.nodes = append(s.nodes, &model.Node{
		ID:         id,
		Name:       name,
		Type:       model.NodeEndpoint,
		Language:   "typescript",
		Path:       s.ctx.filePath,
		Properties: props,
	})
	s.edges = append(s.edges, &model.Edge{
		Source:     s.ctx.modID,
		Target:     id,
		Type:       model.EdgeAPICall,
		Label:      "serves",
		Confidence: 0.9,
	})
}

// svelteKitRoute derives the URL path a route file serves from its folder:
// the folders below "routes", minus "(group)" folders, which organise files
// without adding a path segment. Parameters keep SvelteKit's spelling
// ("/items/[id]"). ok is false for a file that is not under a routes folder.
func svelteKitRoute(ctx fileCtx) (string, bool) {
	dir := filepath.ToSlash(filepath.Dir(ctx.filePath))
	if rel, ok := relToRoot(ctx.scanRoot, ctx.filePath); ok {
		dir = filepath.ToSlash(filepath.Dir(rel))
	}
	segments := strings.Split(dir, "/")
	routes := slices.Index(segments, "routes")
	if routes < 0 {
		return "", false
	}
	parts := slices.DeleteFunc(segments[routes+1:], isRouteGroup)
	return "/" + strings.Join(parts, "/"), true
}

// isRouteGroup recognizes a "(group)" folder.
func isRouteGroup(segment string) bool {
	return strings.HasPrefix(segment, "(") && strings.HasSuffix(segment, ")")
}

// exported is one name a module exports, with the line it is declared on and,
// for "export const x = <value>", the value node.
type exported struct {
	name  string
	line  int
	value *sitter.Node
}

// lineOf returns the 1-based line a node starts on.
func lineOf(n *sitter.Node) int {
	return int(n.StartPoint().Row) + 1
}

// namedChildren returns the named children of n that have the given type.
func namedChildren(n *sitter.Node, nodeType string) []*sitter.Node {
	var out []*sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if child := n.NamedChild(i); child.Type() == nodeType {
			out = append(out, child)
		}
	}
	return out
}

// exportedNames lists the names exported by top-level export statements:
// "export function GET", "export const POST = ...", "export { h as PUT }".
func exportedNames(root *sitter.Node, src []byte) []exported {
	var out []exported
	for _, stmt := range namedChildren(root, "export_statement") {
		if decl := stmt.ChildByFieldName("declaration"); decl != nil {
			out = append(out, declaredNames(decl, src)...)
			continue
		}
		out = append(out, exportClauseNames(stmt, src)...)
	}
	return out
}

// declaredNames returns the names a function or variable declaration binds.
func declaredNames(decl *sitter.Node, src []byte) []exported {
	if decl.Type() == "lexical_declaration" || decl.Type() == "variable_declaration" {
		return variableNames(decl, src)
	}
	if name := decl.ChildByFieldName("name"); name != nil {
		return []exported{{name: name.Content(src), line: lineOf(decl)}}
	}
	return nil
}

// variableNames returns the names bound by "const a = 1, b = 2".
func variableNames(decl *sitter.Node, src []byte) []exported {
	var out []exported
	for _, d := range namedChildren(decl, "variable_declarator") {
		if name := d.ChildByFieldName("name"); name != nil {
			out = append(out, exported{name: name.Content(src), line: lineOf(d), value: d.ChildByFieldName("value")})
		}
	}
	return out
}

// exportClauseNames returns the public names in "export { a, b as c }".
func exportClauseNames(stmt *sitter.Node, src []byte) []exported {
	var out []exported
	for _, clause := range namedChildren(stmt, "export_clause") {
		for _, spec := range namedChildren(clause, "export_specifier") {
			if name := publicName(spec); name != nil {
				out = append(out, exported{name: name.Content(src), line: lineOf(spec)})
			}
		}
	}
	return out
}

// publicName returns the node naming what an export specifier exposes: the
// alias in "h as PUT", the name itself otherwise.
func publicName(spec *sitter.Node) *sitter.Node {
	if alias := spec.ChildByFieldName("alias"); alias != nil {
		return alias
	}
	return spec.ChildByFieldName("name")
}

// objectKeys returns the property names of an object literal, looking through
// "satisfies T", "as T" and parentheses around it.
func objectKeys(value *sitter.Node, src []byte) []exported {
	obj := objectLiteral(value)
	if obj == nil {
		return nil
	}
	var out []exported
	for i := 0; i < int(obj.NamedChildCount()); i++ {
		prop := obj.NamedChild(i)
		if key := propertyKey(prop); key != nil {
			out = append(out, exported{name: stripQuotes(key.Content(src)), line: lineOf(prop)})
		}
	}
	return out
}

// typeWrappers are the expressions an object literal may sit inside.
var typeWrappers = map[string]bool{
	"satisfies_expression": true, "as_expression": true, "parenthesized_expression": true,
}

// objectLiteral unwraps value down to an object literal, or returns nil.
func objectLiteral(value *sitter.Node) *sitter.Node {
	for value != nil && typeWrappers[value.Type()] {
		value = value.NamedChild(0)
	}
	if value == nil || value.Type() != "object" {
		return nil
	}
	return value
}

// propertyKey returns the node naming an object member, or nil for members
// without a static name (spreads, computed keys).
func propertyKey(prop *sitter.Node) *sitter.Node {
	switch prop.Type() {
	case "pair":
		return prop.ChildByFieldName("key")
	case "method_definition":
		return prop.ChildByFieldName("name")
	case "shorthand_property_identifier":
		return prop
	}
	return nil
}
