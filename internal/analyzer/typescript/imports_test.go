package typescript

import (
	"path/filepath"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

// dependencyProps returns the named property of each dependency edge, keyed
// by edge target.
func dependencyProps(edges []*model.Edge, key string) map[string]string {
	out := make(map[string]string)
	for _, e := range edges {
		if e.Type != model.EdgeDependency {
			continue
		}
		if v, ok := e.Properties[key]; ok {
			out[e.Target] = v
		}
	}
	return out
}

func TestAnalyzeRooted_ImportEdgesListNames(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/page.ts": "import Default, { a, b as c } from './one';\n" +
			"import * as two from './two';\n" +
			"import './three';\n" +
			"import type { T } from './one';\n" +
			"import { a } from './one';\n",
		"src/one.ts":   "export const a = 1;\n",
		"src/two.ts":   "export const a = 1;\n",
		"src/three.ts": "export {};\n",
	})
	_, edges, err := New().AnalyzeRooted(root, filepath.Join(root, "src", "page.ts"))
	if err != nil {
		t.Fatalf("AnalyzeRooted failed: %v", err)
	}

	got := dependencyProps(edges, model.EdgeImportedNames)
	want := map[string]string{
		"mod:src/one":   "default,a,b,T", // three statements, one edge, no repeats
		"mod:src/two":   "*",
		"mod:src/three": "", // side-effect import takes nothing
	}
	for target, names := range want {
		if v, ok := got[target]; !ok || v != names {
			t.Errorf("%s names = %q (present %v), want %q", target, v, ok, names)
		}
	}
	if len(got) != len(want) {
		t.Errorf("dependency edges with names = %v, want %d of them", got, len(want))
	}
}

func TestAnalyzeRooted_ReexportsAreDependencies(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/lib/index.ts": "export { default as Bar, helper } from './Bar.svelte';\n" +
			"export * from './components';\n" +
			"export * as utils from './utils';\n" +
			"export {} from './empty';\n" +
			"import { helper as h } from './Bar.svelte';\n" +
			"export const local = 'not-a-module';\n" +
			"export default 'also-not-a-module';\n",
		"src/lib/Bar.svelte":          "<p>bar</p>\n",
		"src/lib/components/index.ts": "export const x = 1;\n",
		"src/lib/utils.ts":            "export const y = 1;\n",
		"src/lib/empty.ts":            "export {};\n",
	})
	_, edges, err := New().AnalyzeRooted(root, filepath.Join(root, "src", "lib", "index.ts"))
	if err != nil {
		t.Fatalf("AnalyzeRooted failed: %v", err)
	}

	reexports := dependencyProps(edges, model.EdgeReexportedNames)
	wantReexports := map[string]string{
		"mod:src/lib/Bar.svelte":       "default=Bar,helper=helper",
		"mod:src/lib/components/index": "*=*",
		"mod:src/lib/utils":            "*=utils",
	}
	if len(reexports) != len(wantReexports) {
		t.Fatalf("re-export edges = %v, want %v", reexports, wantReexports)
	}
	for target, pairs := range wantReexports {
		if reexports[target] != pairs {
			t.Errorf("%s reexports = %q, want %q", target, reexports[target], pairs)
		}
	}

	names := dependencyProps(edges, model.EdgeImportedNames)
	if names["mod:src/lib/Bar.svelte"] != "helper" {
		t.Errorf("import and re-export of one module share an edge; names = %q", names["mod:src/lib/Bar.svelte"])
	}
	if v, ok := names["mod:src/lib/empty"]; !ok || v != "" {
		t.Errorf("export {} from takes nothing; names = %q, present %v", v, ok)
	}

	deps := 0
	for _, e := range edges {
		if e.Type == model.EdgeDependency {
			deps++
		}
	}
	if deps != 4 {
		t.Errorf("want 4 dependency edges (one per module), got %d", deps)
	}
}
