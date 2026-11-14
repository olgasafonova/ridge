package typescript

import (
	"slices"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/olgasafonova/ridge/internal/model"
)

// A module depends on another through an import statement or a re-export
// ("export { a } from './x'", "export * from './x'"). Both are recorded as a
// dependency edge that also lists which names cross it, so the graph can tell
// a page that renders a component from one that only shares a barrel file
// with it.

// moduleRef is one statement's reference to another module.
type moduleRef struct {
	path      string
	names     []string // exported names the statement imports
	reexports []string // "<name there>=<name here>" pairs it re-exports
}

// moduleReference reads an import statement or a re-export. ok is false for
// any other statement, including an export without a "from".
func moduleReference(stmt *sitter.Node, src []byte) (moduleRef, bool) {
	switch stmt.Type() {
	case "import_statement":
		path := extractImportSource(stmt, src)
		return moduleRef{path: path, names: importedNames(stmt, src)}, path != ""
	case "export_statement":
		source := stmt.ChildByFieldName("source")
		if source == nil {
			return moduleRef{}, false
		}
		path := stripQuotes(source.Content(src))
		return moduleRef{path: path, reexports: reexportedNames(stmt, src)}, path != ""
	}
	return moduleRef{}, false
}

// importedNames lists what an import statement takes from its module, by the
// name the module exports it under: "default" for a default import, "*" for a
// namespace import. A side-effect import takes nothing.
func importedNames(stmt *sitter.Node, src []byte) []string {
	var names []string
	for _, clause := range namedChildren(stmt, "import_clause") {
		for i := 0; i < int(clause.NamedChildCount()); i++ {
			names = append(names, clauseNames(clause.NamedChild(i), src)...)
		}
	}
	return names
}

// clauseNames returns the names one part of an import clause takes.
func clauseNames(part *sitter.Node, src []byte) []string {
	switch part.Type() {
	case "identifier":
		return []string{"default"}
	case "namespace_import":
		return []string{"*"}
	case "named_imports":
		var names []string
		for _, spec := range namedChildren(part, "import_specifier") {
			if name := spec.ChildByFieldName("name"); name != nil {
				names = append(names, name.Content(src))
			}
		}
		return names
	}
	return nil
}

// reexportedNames lists what "export ... from" passes on, as
// "<name in the other module>=<name in this one>" pairs. "export * from" is
// "*=*" and "export * as ns from" is "*=ns".
func reexportedNames(stmt *sitter.Node, src []byte) []string {
	if clauses := namedChildren(stmt, "export_clause"); len(clauses) > 0 {
		return clausePairs(clauses, src)
	}
	for _, ns := range namedChildren(stmt, "namespace_export") {
		if ns.NamedChildCount() > 0 {
			return []string{"*=" + ns.NamedChild(0).Content(src)}
		}
	}
	return []string{"*=*"}
}

// clausePairs returns one pair per name in "export { a, b as c } from".
func clausePairs(clauses []*sitter.Node, src []byte) []string {
	var pairs []string
	for _, clause := range clauses {
		for _, spec := range namedChildren(clause, "export_specifier") {
			name, public := spec.ChildByFieldName("name"), publicName(spec)
			if !anyNil(name, public) {
				pairs = append(pairs, name.Content(src)+"="+public.Content(src))
			}
		}
	}
	return pairs
}

// recordNames adds a statement's names to the edge for its module. Several
// statements may reference one module; the graph keeps a single edge per
// source and target, so the names are merged onto it.
func recordNames(edge *model.Edge, ref moduleRef) {
	if edge.Properties == nil {
		edge.Properties = make(map[string]string)
	}
	if ref.reexports == nil {
		// Also for a side-effect import: an empty list says it takes nothing.
		mergeList(edge.Properties, model.EdgeImportedNames, ref.names)
	}
	if len(ref.reexports) > 0 {
		mergeList(edge.Properties, model.EdgeReexportedNames, ref.reexports)
	}
}

// mergeList appends values to a comma-separated property, skipping ones
// already there.
func mergeList(props map[string]string, key string, values []string) {
	var list []string
	if current := props[key]; current != "" {
		list = strings.Split(current, ",")
	}
	for _, v := range values {
		if !slices.Contains(list, v) {
			list = append(list, v)
		}
	}
	props[key] = strings.Join(list, ",")
}
