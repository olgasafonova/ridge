package model

import (
	"sort"
	"testing"
)

func moduleNode(id, folder string) *Node {
	return &Node{ID: id, Type: NodeModule, Path: folder}
}

func actionNode(id, file, route, name string) *Node {
	return &Node{ID: id, Type: NodeEndpoint, Path: file, Properties: map[string]string{
		"method": "post", "route": route, "action": name,
	}}
}

func importEdge(from, to string, props map[string]string) *Edge {
	return &Edge{Source: from, Target: to, Type: EdgeDependency, Properties: props}
}

// resolvedTargets returns the sorted targets of the resolved edges that
// leave source with the given label.
func resolvedTargets(g *ArchGraph, source, label string) []string {
	var out []string
	for _, e := range g.ResolvedEdges() {
		if e.Source == source && e.Label == label {
			out = append(out, e.Target)
		}
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestActionTarget(t *testing.T) {
	if got := ActionTarget("", "assign"); got != "action:?/assign" {
		t.Fatalf("relative: %q", got)
	}
	if got := ActionTarget("/login", "reset"); got != "action:/login?/reset" {
		t.Fatalf("with a path: %q", got)
	}
}

// formGraph is a small SvelteKit app. Bar.svelte holds a form posting to
// "?/assign". Two pages declare an "assign" action and both import from the
// same barrel file, but only the case page takes Bar from it.
func formGraph() *ArchGraph {
	g := NewGraph("/app")
	g.AddNode(moduleNode("mod:lib/Bar.svelte", "/app/lib"))
	g.AddNode(moduleNode("mod:lib/Other.svelte", "/app/lib"))
	g.AddNode(moduleNode("mod:lib/components/index", "/app/lib/components"))
	g.AddNode(moduleNode("mod:lib/index", "/app/lib"))
	g.AddNode(moduleNode("mod:routes/cases/[id]/+page.svelte", "/app/routes/cases/[id]"))
	g.AddNode(moduleNode("mod:routes/cases/+page.svelte", "/app/routes/cases"))
	g.AddNode(actionNode("endpoint:case:assign", "/app/routes/cases/[id]/+page.server.ts", "/cases/[id]", "assign"))
	g.AddNode(actionNode("endpoint:list:assign", "/app/routes/cases/+page.server.ts", "/cases", "assign"))
	g.AddNode(actionNode("endpoint:list:review", "/app/routes/cases/+page.server.ts", "/cases", "review"))

	g.AddEdge(importEdge("mod:lib/components/index", "mod:lib/Bar.svelte", map[string]string{EdgeReexportedNames: "default=Bar"}))
	g.AddEdge(importEdge("mod:lib/components/index", "mod:lib/Other.svelte", map[string]string{EdgeReexportedNames: "default=Other"}))
	g.AddEdge(importEdge("mod:lib/index", "mod:lib/components/index", map[string]string{EdgeReexportedNames: "*=*"}))
	g.AddEdge(importEdge("mod:routes/cases/[id]/+page.svelte", "mod:lib/index", map[string]string{EdgeImportedNames: "Bar,Other"}))
	g.AddEdge(importEdge("mod:routes/cases/+page.svelte", "mod:lib/index", map[string]string{EdgeImportedNames: "Other"}))
	// Imports of packages and a self-reference are not paths to a page.
	g.AddEdge(importEdge("mod:lib/Bar.svelte", "import:svelte", map[string]string{EdgeImportedNames: "onMount"}))
	g.AddEdge(importEdge("mod:lib/Bar.svelte", "mod:lib/Bar.svelte", nil))
	return g
}

func TestResolvedEdges_FormActionFollowsNamedImports(t *testing.T) {
	g := formGraph()
	g.AddEdge(&Edge{Source: "mod:lib/Bar.svelte", Target: ActionTarget("", "assign"), Type: EdgeAPICall, Label: "form"})

	got := resolvedTargets(g, "mod:lib/Bar.svelte", "form")
	if !equalStrings(got, []string{"endpoint:case:assign"}) {
		t.Fatalf("got %v; the list page shares the barrel but does not import Bar", got)
	}
}

func TestResolvedEdges_FormActionOnEveryRenderingPage(t *testing.T) {
	g := formGraph()
	g.AddEdge(&Edge{Source: "mod:lib/Other.svelte", Target: ActionTarget("", "assign"), Type: EdgeAPICall, Label: "form"})

	got := resolvedTargets(g, "mod:lib/Other.svelte", "form")
	if !equalStrings(got, []string{"endpoint:case:assign", "endpoint:list:assign"}) {
		t.Fatalf("got %v; both pages import Other", got)
	}
}

func TestResolvedEdges_FormActionCases(t *testing.T) {
	tests := []struct {
		name   string
		source string
		target string
		want   []string
	}{
		{"form in the page itself", "mod:routes/cases/+page.svelte", ActionTarget("", "review"), []string{"endpoint:list:review"}},
		{"action the rendering page does not declare", "mod:lib/Bar.svelte", ActionTarget("", "review"), nil},
		{"component nobody imports", "mod:lib/Lonely.svelte", ActionTarget("", "assign"), nil},
		{"path names the page", "mod:lib/Lonely.svelte", ActionTarget("/cases/42", "assign"), []string{"endpoint:case:assign"}},
		{"literal route beats a parameter", "mod:lib/Lonely.svelte", ActionTarget("/cases", "assign"), []string{"endpoint:list:assign"}},
		{"path no page serves", "mod:lib/Lonely.svelte", ActionTarget("/nowhere", "assign"), nil},
		{"source is not a node", "mod:missing", ActionTarget("", "assign"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := formGraph()
			g.AddNode(moduleNode("mod:lib/Lonely.svelte", "/app/lib"))
			g.AddEdge(&Edge{Source: tt.source, Target: tt.target, Type: EdgeAPICall, Label: "form"})
			if got := resolvedTargets(g, tt.source, "form"); !equalStrings(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// An importer whose analyzer records no names counts as using everything.
func TestResolvedEdges_FormActionThroughUntrackedImport(t *testing.T) {
	g := NewGraph("/app")
	g.AddNode(moduleNode("mod:lib/Bar.svelte", "/app/lib"))
	g.AddNode(moduleNode("mod:routes/+page.svelte", "/app/routes"))
	g.AddNode(actionNode("endpoint:home:save", "/app/routes/+page.server.ts", "/", "save"))
	g.AddEdge(importEdge("mod:routes/+page.svelte", "mod:lib/Bar.svelte", nil))
	g.AddEdge(&Edge{Source: "mod:lib/Bar.svelte", Target: ActionTarget("", "save"), Type: EdgeAPICall, Label: "form"})

	if got := resolvedTargets(g, "mod:lib/Bar.svelte", "form"); !equalStrings(got, []string{"endpoint:home:save"}) {
		t.Fatalf("got %v", got)
	}
}

func TestImporterEdgeCarried(t *testing.T) {
	tests := []struct {
		name    string
		props   map[string]string
		exposed nameSet
		want    []string
	}{
		{"untracked import", nil, nameSet{"Bar": true}, []string{"*"}},
		{"imports the name", map[string]string{EdgeImportedNames: "Bar"}, nameSet{"Bar": true}, []string{"*"}},
		{"imports another name", map[string]string{EdgeImportedNames: "Baz"}, nameSet{"Bar": true}, nil},
		{"namespace import", map[string]string{EdgeImportedNames: "*"}, nameSet{"Bar": true}, []string{"*"}},
		{"side-effect import", map[string]string{EdgeImportedNames: ""}, nameSet{"*": true}, nil},
		{"re-export renames", map[string]string{EdgeReexportedNames: "default=Bar"}, nameSet{"*": true}, []string{"Bar"}},
		{"re-export of another name", map[string]string{EdgeReexportedNames: "Baz=Baz"}, nameSet{"Bar": true}, nil},
		{"export star drops default", map[string]string{EdgeReexportedNames: "*=*"}, nameSet{"Bar": true, "default": true}, []string{"Bar"}},
		{"export star as namespace", map[string]string{EdgeReexportedNames: "*=ns"}, nameSet{"Bar": true}, []string{"ns"}},
		{"malformed pair is ignored", map[string]string{EdgeReexportedNames: "Bar"}, nameSet{"Bar": true}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			carried := newImporterEdge(importEdge("mod:a", "mod:b", tt.props)).carried(tt.exposed)
			var got []string
			for name := range carried {
				got = append(got, name)
			}
			sort.Strings(got)
			if !equalStrings(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// Two endpoints for one action in one folder should not happen; if it does,
// the lowest ID is the one a form resolves to, on every run.
func TestResolvedEdges_FormActionDuplicateEndpoint(t *testing.T) {
	g := NewGraph("/app")
	g.AddNode(moduleNode("mod:routes/+page.svelte", "/app/routes"))
	g.AddNode(actionNode("endpoint:b", "/app/routes/+page.server.ts", "/", "save"))
	g.AddNode(actionNode("endpoint:a", "/app/routes/+page.server.js", "/", "save"))
	g.AddEdge(&Edge{Source: "mod:routes/+page.svelte", Target: ActionTarget("", "save"), Type: EdgeAPICall, Label: "form"})

	if got := resolvedTargets(g, "mod:routes/+page.svelte", "form"); !equalStrings(got, []string{"endpoint:a"}) {
		t.Fatalf("got %v", got)
	}
}
