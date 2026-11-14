package typescript

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func TestSvelteScriptSource(t *testing.T) {
	src := "<script module lang=\"ts\">\n" +
		"  export const prerender = true;\n" +
		"</script>\n" +
		"<script lang=\"ts\" generics=\"T extends Record<string, unknown>\">\n" +
		"  import Child from './Child.svelte';\n" +
		"</script>\n" +
		"<scripted-thing>import no from 'markup';</scripted-thing>\n" +
		"<Child>é</Child>\n" +
		"<style>p { color: red; }</style>\n"
	got := string(svelteScriptSource([]byte(src)))

	if len(got) != len(src) {
		t.Fatalf("length changed: %d -> %d", len(src), len(got))
	}
	if strings.Count(got, "\n") != strings.Count(src, "\n") {
		t.Fatal("line breaks changed")
	}
	want := []string{"export const prerender = true;", "import Child from './Child.svelte';"}
	if fields := strings.Split(strings.Join(strings.Fields(got), " "), "; "); len(fields) != len(want) {
		t.Fatalf("kept more than the two script bodies: %q", strings.Join(strings.Fields(got), " "))
	}
	for _, w := range want {
		if strings.Index(got, w) != strings.Index(src, w) {
			t.Fatalf("%q moved or is missing", w)
		}
	}
}

func TestSvelteScriptSource_NoScript(t *testing.T) {
	for _, src := range []string{"", "<p>hello</p>\n", "<script lang=\"ts\">\nlet a = 1;\n", "<script"} {
		if got := string(svelteScriptSource([]byte(src))); strings.TrimSpace(got) != "" {
			t.Fatalf("svelteScriptSource(%q) = %q, want blank", src, got)
		}
	}
}

func TestAnalyzeRooted_SvelteComponent(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/routes/+page.svelte": "<script lang=\"ts\">\n" +
			"\timport Gauge from '$lib/Gauge.svelte';\n" +
			"\timport { counter } from '$lib/counter.svelte';\n" +
			"\timport { helper } from './helper';\n" +
			"\tasync function trace() {\n" +
			"\t\tawait fetch('/api/trace', { method: 'POST' });\n" +
			"\t}\n" +
			"</script>\n" +
			"<Gauge on:click={trace} />\n",
		"src/routes/+page.ts":       "export const ssr = false;\n",
		"src/routes/helper.ts":      "export const helper = 1;\n",
		"src/lib/Gauge.svelte":      "<p>no script here</p>\n",
		"src/lib/counter.svelte.ts": "export const counter = 1;\n",
		"package.json":              `{"devDependencies": {"@sveltejs/kit": "^2.0.0"}}`,
	})
	a := New()
	nodes, edges, err := a.AnalyzeRooted(root, filepath.Join(root, "src", "routes", "+page.svelte"))
	if err != nil {
		t.Fatalf("AnalyzeRooted failed: %v", err)
	}
	mod := moduleNode(t, nodes)
	if mod.ID != "mod:src/routes/+page.svelte" || mod.Name != "+page.svelte" || mod.Language != "svelte" {
		t.Fatalf("module = %+v", mod)
	}

	targets := dependencyTargets(edges)
	for _, want := range []string{"mod:src/lib/Gauge.svelte", "mod:src/lib/counter.svelte.ts", "mod:src/routes/helper"} {
		if !targets[want] {
			t.Fatalf("missing dependency target %q; got %v", want, targets)
		}
	}

	var call *model.Edge
	for _, e := range edges {
		if e.Type == model.EdgeAPICall {
			call = e
		}
	}
	if call == nil || call.Target != "route:POST /api/trace" || call.Properties["line"] != "6" {
		t.Fatalf("api call edge = %+v", call)
	}

	// +page.ts beside +page.svelte must not share its ID.
	tsNodes, _, err := a.AnalyzeRooted(root, filepath.Join(root, "src", "routes", "+page.ts"))
	if err != nil {
		t.Fatalf("AnalyzeRooted failed: %v", err)
	}
	if id := moduleNode(t, tsNodes).ID; id != "mod:src/routes/+page" {
		t.Fatalf("+page.ts module ID = %q", id)
	}
}
