package model

import "testing"

// Bead r0h: a target that several nodes match used to go to whichever node
// the map handed out first. Resolving many times catches a random pick.
func TestResolvedEdges_AmbiguousTargetsResolveTheSameWayEveryRun(t *testing.T) {
	build := func() *ArchGraph {
		g := NewGraph("/repo")
		g.AddNode(&Node{ID: "pkg:caller", Type: NodePackage, Path: "/repo/cmd"})
		// An import of .../internal/db matches both of these by path tail.
		g.AddNode(&Node{ID: "pkg:a-top-level-db", Type: NodePackage, Path: "/repo/db"})
		g.AddNode(&Node{ID: "pkg:internal/db", Type: NodePackage, Path: "/repo/internal/db"})
		// Two nodes in one folder: only the ID tells them apart.
		g.AddNode(&Node{ID: "mod:util/b", Type: NodeModule, Path: "/repo/util"})
		g.AddNode(&Node{ID: "mod:util/a", Type: NodeModule, Path: "/repo/util"})
		// Three notes named plan.
		g.AddNode(&Node{ID: "note:archive/2024/plan", Type: NodeNote, Path: "/repo/archive/2024/plan.md"})
		g.AddNode(&Node{ID: "note:plan", Type: NodeNote, Path: "/repo/plan.md"})
		g.AddNode(&Node{ID: "note:work/plan", Type: NodeNote, Path: "/repo/work/plan.markdown"})

		for _, target := range []string{
			"import:github.com/acme/app/internal/db",
			"import:github.com/acme/app/util",
			"import:github.com/acme/app/missing",
			"wikilink:plan",
			"wikilink:2024/plan",
			"wikilink:work/plan",
			"wikilink:nothing",
		} {
			g.AddEdge(&Edge{Source: "pkg:caller", Target: target, Type: EdgeDependency, Label: target})
		}
		return g
	}
	want := map[string]string{
		"import:github.com/acme/app/internal/db": "pkg:internal/db",        // longest path tail
		"import:github.com/acme/app/util":        "mod:util/a",             // same path: lowest ID
		"wikilink:plan":                          "note:plan",              // closest to the root
		"wikilink:2024/plan":                     "note:archive/2024/plan", // names part of the path
		"wikilink:work/plan":                     "note:work/plan",         // names the whole path
	}
	for run := 0; run < 50; run++ {
		got := make(map[string]string)
		for _, e := range build().ResolvedEdges() {
			got[e.Label] = e.Target
		}
		if len(got) != len(want) {
			t.Fatalf("run %d: resolved %v, want %v", run, got, want)
		}
		for label, target := range want {
			if got[label] != target {
				t.Fatalf("run %d: %s resolved to %q, want %q", run, label, got[label], target)
			}
		}
	}
}
