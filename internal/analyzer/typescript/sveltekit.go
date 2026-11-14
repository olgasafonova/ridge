package typescript

import (
	"fmt"
	"path/filepath"
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

	var nodes []*model.Node
	var edges []*model.Edge
	add := func(method, name string, props map[string]string, line int) {
		id := ctx.locatedID("endpoint", line)
		props["method"] = strings.ToLower(method)
		props["route"] = route
		props["line"] = fmt.Sprintf("%d", line)
		props["framework"] = "sveltekit"
		nodes = append(nodes, &model.Node{
			ID:         id,
			Name:       name,
			Type:       model.NodeEndpoint,
			Language:   "typescript",
			Path:       ctx.filePath,
			Properties: props,
		})
		edges = append(edges, &model.Edge{
			Source:     ctx.modID,
			Target:     id,
			Type:       model.EdgeAPICall,
			Label:      "serves",
			Confidence: 0.9,
		})
	}

	for _, exp := range exportedNames(root, ctx.src) {
		switch {
		case base == "+server" && svelteKitVerbs[exp.name]:
			add(exp.name, exp.name+" "+route, map[string]string{}, exp.line)
		case base == "+page.server" && exp.name == "actions":
			for _, action := range objectKeys(exp.value, ctx.src) {
				name := "POST " + route
				if action.name != "default" {
					name += "?/" + action.name
				}
				add("POST", name, map[string]string{"action": action.name}, action.line)
			}
		}
	}
	return nodes, edges
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
	start := -1
	for i, seg := range segments {
		if seg == "routes" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", false
	}
	var parts []string
	for _, seg := range segments[start:] {
		if strings.HasPrefix(seg, "(") && strings.HasSuffix(seg, ")") {
			continue
		}
		parts = append(parts, seg)
	}
	return "/" + strings.Join(parts, "/"), true
}

// exported is one name a module exports, with the line it is declared on and,
// for "export const x = <value>", the value node.
type exported struct {
	name  string
	line  int
	value *sitter.Node
}

// exportedNames lists the names exported by top-level export statements:
// "export function GET", "export const POST = ...", "export { h as PUT }".
func exportedNames(root *sitter.Node, src []byte) []exported {
	var out []exported
	for i := 0; i < int(root.NamedChildCount()); i++ {
		stmt := root.NamedChild(i)
		if stmt.Type() != "export_statement" {
			continue
		}
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
	line := int(decl.StartPoint().Row) + 1
	if decl.Type() != "lexical_declaration" && decl.Type() != "variable_declaration" {
		if name := decl.ChildByFieldName("name"); name != nil {
			return []exported{{name: name.Content(src), line: line}}
		}
		return nil
	}
	var out []exported
	for i := 0; i < int(decl.NamedChildCount()); i++ {
		d := decl.NamedChild(i)
		if d.Type() != "variable_declarator" {
			continue
		}
		if name := d.ChildByFieldName("name"); name != nil {
			out = append(out, exported{
				name:  name.Content(src),
				line:  int(d.StartPoint().Row) + 1,
				value: d.ChildByFieldName("value"),
			})
		}
	}
	return out
}

// exportClauseNames returns the public names in "export { a, b as c }".
func exportClauseNames(stmt *sitter.Node, src []byte) []exported {
	var out []exported
	for i := 0; i < int(stmt.NamedChildCount()); i++ {
		clause := stmt.NamedChild(i)
		if clause.Type() != "export_clause" {
			continue
		}
		for j := 0; j < int(clause.NamedChildCount()); j++ {
			spec := clause.NamedChild(j)
			name := spec.ChildByFieldName("alias")
			if name == nil {
				name = spec.ChildByFieldName("name")
			}
			if name != nil {
				out = append(out, exported{name: name.Content(src), line: int(spec.StartPoint().Row) + 1})
			}
		}
	}
	return out
}

// objectKeys returns the property names of an object literal, looking through
// "satisfies T", "as T" and parentheses around it.
func objectKeys(value *sitter.Node, src []byte) []exported {
	for value != nil && value.Type() != "object" {
		switch value.Type() {
		case "satisfies_expression", "as_expression", "parenthesized_expression":
			value = value.NamedChild(0)
		default:
			return nil
		}
	}
	if value == nil {
		return nil
	}
	var out []exported
	for i := 0; i < int(value.NamedChildCount()); i++ {
		prop := value.NamedChild(i)
		var key *sitter.Node
		switch prop.Type() {
		case "pair":
			key = prop.ChildByFieldName("key")
		case "method_definition":
			key = prop.ChildByFieldName("name")
		case "shorthand_property_identifier":
			key = prop
		}
		if key != nil {
			out = append(out, exported{name: stripQuotes(key.Content(src)), line: int(prop.StartPoint().Row) + 1})
		}
	}
	return out
}
