package model

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// A SvelteKit form posts to a form action of the page it is rendered on:
// <form method="POST" action="?/assign"> calls the "assign" action declared
// in that page's +page.server.ts. The form is often in a shared component, so
// the file holding it does not say which page that is. Analyzers emit the
// call as an edge to an "action:<path>?/<name>" placeholder, with an empty
// path for "the current page". ResolvedEdges rewrites it to the endpoint of
// each page that renders the component, following import edges upwards.

// actionTargetPrefix marks an edge target that names a form action.
const actionTargetPrefix = "action:"

// actionSeparator is how a URL names a form action: "/cases?/assign".
const actionSeparator = "?/"

// anyName stands for every name a module exports.
const anyName = "*"

// Edge properties on a dependency edge, written by analyzers that know which
// names an import takes. Without them an import counts as taking everything.
const (
	// EdgeImportedNames lists the exported names the source imports from the
	// target, comma-separated: "CaseBar,default". "*" is a namespace import.
	EdgeImportedNames = "names"
	// EdgeReexportedNames lists what the source re-exports from the target,
	// comma-separated "<name in target>=<name in source>" pairs.
	// "*=*" is "export * from"; "*=ns" is "export * as ns from".
	EdgeReexportedNames = "reexports"
)

// ActionTarget builds the placeholder edge target for a form action. path is
// the page's route, or "" for the page the form is rendered on.
func ActionTarget(path, name string) string {
	return actionTargetPrefix + path + actionSeparator + name
}

// nameSet is a set of exported names; anyName in it matches every name.
type nameSet map[string]bool

func (s nameSet) has(name string) bool {
	return s[anyName] || s[name]
}

// add puts names into s and reports whether any of them was new.
func (s nameSet) add(names nameSet) bool {
	grew := false
	for n := range names {
		if !s[n] {
			s[n] = true
			grew = true
		}
	}
	return grew
}

// importerEdge is one module's import of another, seen from the imported side.
type importerEdge struct {
	from      string
	tracked   bool // the analyzer recorded which names the import takes
	names     []string
	reexports []reexport
}

// reexport is one clause of "export ... from": what it takes from the other
// module and the name it passes it on under.
type reexport struct {
	from string // name in the imported module; anyName for "export *"
	as   string // name in the importing module; anyName for a plain "export *"
}

func newImporterEdge(e *Edge) importerEdge {
	ie := importerEdge{from: e.Source}
	names, hasNames := e.Properties[EdgeImportedNames]
	reexports, hasReexports := e.Properties[EdgeReexportedNames]
	ie.tracked = hasNames || hasReexports
	ie.names = splitList(names)
	for _, pair := range splitList(reexports) {
		if from, as, ok := strings.Cut(pair, "="); ok {
			ie.reexports = append(ie.reexports, reexport{from: from, as: as})
		}
	}
	return ie
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// carried returns the names under which the importing module passes on what
// the imported module exposes as the given names. Empty means the importer
// takes something else from that module and never touches it.
func (ie importerEdge) carried(exposed nameSet) nameSet {
	if !ie.tracked {
		return nameSet{anyName: true}
	}
	out := nameSet{}
	for _, name := range ie.names {
		if name == anyName || exposed.has(name) {
			// The importer uses it, so whoever imports the importer may too.
			return nameSet{anyName: true}
		}
	}
	for _, clause := range ie.reexports {
		out.add(clause.carried(exposed))
	}
	return out
}

// carried returns the names the clause passes on.
func (x reexport) carried(exposed nameSet) nameSet {
	switch {
	case x.from != anyName:
		if exposed.has(x.from) {
			return nameSet{x.as: true}
		}
		return nil
	case x.as != anyName:
		return nameSet{x.as: true} // export * as ns
	}
	// export * passes every name on except the default export.
	out := nameSet{}
	for name := range exposed {
		if name != "default" {
			out[name] = true
		}
	}
	return out
}

// actionEndpoint is an endpoint node that serves a form action.
type actionEndpoint struct {
	id       string
	name     string
	segments []string
}

// actionResolver maps form-action placeholders to endpoint node IDs.
type actionResolver struct {
	nodes     map[string]*Node
	edges     []*Edge
	imports   *targetResolver
	endpoints []actionEndpoint
	byFolder  map[string]map[string]string // folder -> action name -> endpoint ID
	importers map[string][]importerEdge    // built on first use
}

func newActionResolver(nodes map[string]*Node, edges []*Edge, imports *targetResolver) *actionResolver {
	r := &actionResolver{nodes: nodes, edges: edges, imports: imports, byFolder: make(map[string]map[string]string)}
	ids := make([]string, 0, len(nodes))
	for id, n := range nodes {
		if n.Type == NodeEndpoint && n.Properties["action"] != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids) // map order is random; the lowest ID wins a tie
	for _, id := range ids {
		r.index(nodes[id])
	}
	return r
}

func (r *actionResolver) index(n *Node) {
	name := n.Properties["action"]
	r.endpoints = append(r.endpoints, actionEndpoint{id: n.ID, name: name, segments: routeSegments(n.Properties["route"])})
	folder := filepath.Dir(n.Path)
	if r.byFolder[folder] == nil {
		r.byFolder[folder] = make(map[string]string)
	}
	if _, taken := r.byFolder[folder][name]; !taken {
		r.byFolder[folder][name] = n.ID
	}
}

// formAction is one use of a form action: the module whose markup names it,
// the page path written in front of it ("" for the page the form is rendered
// on) and the action's name.
type formAction struct {
	source string
	path   string
	name   string
}

// resolve returns the endpoints an edge's form action reaches. ok is false
// for an edge whose target is not a form-action placeholder.
func (r *actionResolver) resolve(e *Edge) (ids []string, ok bool) {
	spec, isAction := strings.CutPrefix(e.Target, actionTargetPrefix)
	if !isAction {
		return nil, false
	}
	fa := formAction{source: e.Source}
	fa.path, fa.name, _ = strings.Cut(spec, actionSeparator)
	if fa.path != "" {
		return r.onNamedPage(fa), true
	}
	return r.onRenderingPages(fa), true
}

// onNamedPage finds the action of the page that serves the path in front of it.
func (r *actionResolver) onNamedPage(fa formAction) []string {
	called := routeSegments(fa.path)
	bestID, best := "", -1
	for _, ep := range r.endpoints {
		if ep.name != fa.name {
			continue
		}
		if literal, ok := matchRoute(ep.segments, called); ok && literal > best {
			bestID, best = ep.id, literal
		}
	}
	if bestID == "" {
		return nil
	}
	return []string{bestID}
}

// onRenderingPages walks from the module holding the form to every module
// that imports it, directly or through re-exports, and collects the action
// of each one that sits in a folder declaring it. The walk stops at such a
// module: a page is where a relative form action lands.
func (r *actionResolver) onRenderingPages(fa formAction) []string {
	walk := importWalk{exposed: map[string]nameSet{fa.source: {anyName: true}}}
	queue := []string{fa.source}
	var found []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if ep := r.actionBeside(r.nodes[id], fa); ep != "" {
			found = append(found, ep)
			continue
		}
		queue = append(queue, walk.reach(r.importersOf(id), walk.exposed[id])...)
	}
	sort.Strings(found)
	return slices.Compact(found)
}

// importWalk tracks, for each module reached while following imports
// upwards, the names under which it exposes the thing being followed.
type importWalk struct {
	exposed map[string]nameSet
}

// reach records what each importer takes and returns the importers that
// expose it under a name not seen before, which are the ones to visit.
func (w importWalk) reach(importers []importerEdge, exposed nameSet) []string {
	var next []string
	for _, ie := range importers {
		carried := ie.carried(exposed)
		if len(carried) == 0 {
			continue
		}
		if w.exposed[ie.from] == nil {
			w.exposed[ie.from] = nameSet{}
		}
		if w.exposed[ie.from].add(carried) {
			next = append(next, ie.from)
		}
	}
	return next
}

// actionBeside returns the endpoint of the action declared in the module's
// own folder, or "".
func (r *actionResolver) actionBeside(module *Node, fa formAction) string {
	if module == nil {
		return ""
	}
	return r.byFolder[module.Path][fa.name]
}

func (r *actionResolver) importersOf(id string) []importerEdge {
	if r.importers == nil {
		r.importers = make(map[string][]importerEdge)
		for _, e := range r.edges {
			if e.Type != EdgeDependency {
				continue
			}
			target, ok := r.imports.resolve(e.Target)
			if !ok || r.nodes[target] == nil {
				continue
			}
			r.importers[target] = append(r.importers[target], newImporterEdge(e))
		}
	}
	return r.importers[id]
}
