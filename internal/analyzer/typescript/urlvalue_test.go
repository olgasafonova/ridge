package typescript

import (
	"path/filepath"
	"sort"
	"testing"
)

func TestAnalyzeRooted_FetchWithVariableURL(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/lib/geo.ts": "const API = 'https://api.example.com';\n" +
			"export const PATH = '/api/analyze';\n" +
			"let mutable = '/api/mutable';\n" +
			"\n" +
			"export async function search(q: string) {\n" +
			"  const url =\n" +
			"    'https://ws.example.no/v1/sok' +\n" +
			"    `?sok=${encodeURIComponent(q)}` +\n" +
			"    '&size=8';\n" +
			"  await fetch(url);\n" +
			"}\n" +
			"export async function parcel(id: string) {\n" +
			"  const url = `${API}/parcels/${id}`;\n" +
			"  await fetch(url);\n" +
			"}\n" +
			"export async function analyze() {\n" +
			"  await fetch(PATH, { method: 'POST' });\n" +
			"  await fetch(('/api/items/' + id));\n" +
			"}\n" +
			"export async function shadowed(PATH: string) {\n" +
			"  await fetch(PATH);\n" +
			"}\n" +
			"export const arrow = async (API) => fetch(API);\n" +
			"export async function unknowable(q: string) {\n" +
			"  await fetch(mutable);\n" +
			"  await fetch(q + suffix);\n" +
			"  await fetch(notDeclared);\n" +
			"  await fetch(a - b);\n" +
			"  const built = new URL('/api/built', location.origin);\n" +
			"  await fetch(built);\n" +
			"  const later = '/api/later';\n" +
			"  for (const PATH of paths) { await fetch(PATH); }\n" +
			"  try { await fetch(q); } catch (API) { await fetch(API); }\n" +
			"}\n" +
			"export async function declaredAfterUse() {\n" +
			"  await fetch(tooLate);\n" +
			"  const tooLate = '/api/too-late';\n" +
			"}\n",
	})
	_, edges, err := New().AnalyzeRooted(root, filepath.Join(root, "src", "lib", "geo.ts"))
	if err != nil {
		t.Fatalf("AnalyzeRooted failed: %v", err)
	}

	var got []string
	for target := range apiCallTargets(edges) {
		got = append(got, target)
	}
	sort.Strings(got)
	want := []string{
		"route:GET /api/built",
		"route:GET /api/items/*",
		"route:GET /api/mutable", // a let nothing assigns to again
		"route:POST /api/analyze",
		"service:api.example.com",
		"service:ws.example.no",
	}
	if !equalStrings(got, want) {
		t.Fatalf("api call targets = %v, want %v", got, want)
	}
}

func TestAnalyzeRooted_FetchWithURLObjectsAndLets(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/lib/wms.ts": "const BASE = 'https://maps.example.no/wms';\n" +
			"let reassigned = '/api/first';\n" +
			"reassigned = '/api/second';\n" +
			"export async function calls(layer: { url: string }, base: string) {\n" +
			"  const wms = new URL(BASE);\n" +
			"  wms.searchParams.append('request', 'GetMap');\n" +
			"  await fetch(wms.toString());\n" +
			"  const joined = new URL('/v2/tiles', BASE);\n" +
			"  await fetch(joined.href);\n" +
			"  await fetch(String(new URL('/api/page', window.location.href)));\n" +
			"  let path = '/api/let';\n" +
			"  await fetch(path, { method: 'POST' });\n" +
			"  await fetch(new URL('https://abs.example.com/x', base));\n" +
			"  // Not knowable from the file:\n" +
			"  await fetch(new URL(layer.url).toString());\n" +
			"  await fetch(new URL('/api/unknown-base', base));\n" +
			"  await fetch(new URL('relative', BASE));\n" +
			"  await fetch(new Request('/api/request'));\n" +
			"  await fetch(new URL());\n" +
			"  await fetch(reassigned);\n" +
			"  let counter = '/api/count';\n" +
			"  counter += '/more';\n" +
			"  await fetch(counter);\n" +
			"  let moved = new URL('/api/moved', location.origin);\n" +
			"  moved.pathname = '/api/elsewhere';\n" +
			"  await fetch(moved);\n" +
			"  await fetch(wms.toString('x'));\n" +
			"  await fetch(wms.origin);\n" +
			"  await fetch(wms.toJSON());\n" +
			"  await fetch(String(wms, 'x'));\n" +
			"  await fetch(make()());\n" +
			"}\n",
	})
	_, edges, err := New().AnalyzeRooted(root, filepath.Join(root, "src", "lib", "wms.ts"))
	if err != nil {
		t.Fatalf("AnalyzeRooted failed: %v", err)
	}

	var got []string
	for target := range apiCallTargets(edges) {
		got = append(got, target)
	}
	sort.Strings(got)
	want := []string{
		"route:GET /api/page",
		"route:POST /api/let",
		"service:abs.example.com",
		"service:maps.example.no",
	}
	if !equalStrings(got, want) {
		t.Fatalf("api call targets = %v, want %v", got, want)
	}
	urls := make(map[string]bool)
	for _, e := range edges {
		urls[e.Properties["url"]] = true
	}
	for _, u := range []string{"https://maps.example.no/wms", "https://maps.example.no/v2/tiles"} {
		if !urls[u] {
			t.Errorf("no call to %s; urls = %v", u, urls)
		}
	}
}

func TestURLValue_FollowsConstChainsUpToTheHopLimit(t *testing.T) {
	src := "const a = '/api/deep';\nconst b = a;\nconst c = b;\nconst d = c;\nconst e = d;\nfetch(d);\nfetch(e);\n"
	root := parseTS(t, src)
	var values []string
	for i := 0; i < int(root.NamedChildCount()); i++ {
		stmt := root.NamedChild(i)
		if stmt.Type() != "expression_statement" {
			continue
		}
		v, _ := firstURLArg(stmt.NamedChild(0), []byte(src))
		values = append(values, v)
	}
	if !equalStrings(values, []string{"/api/deep", ""}) {
		t.Fatalf("got %q; four bindings are followed, a fifth is not", values)
	}
}

func TestURLValue_EmptyShapes(t *testing.T) {
	src := "fetch(());\nfetch(`${}`);\n"
	root := parseTS(t, src)
	for i := 0; i < int(root.NamedChildCount()); i++ {
		stmt := root.NamedChild(i)
		if stmt.Type() != "expression_statement" || stmt.NamedChildCount() == 0 {
			continue
		}
		// Malformed input must give an answer, not a panic.
		_, _ = firstURLArg(stmt.NamedChild(0), []byte(src))
	}
}

// The helpers are handed whatever the parser produced, which for broken
// source can be a node without the children a well-formed one has.
func TestURLHelpers_NodesWithoutChildren(t *testing.T) {
	src := "x;\n"
	leaf := parseTS(t, src).NamedChild(0).NamedChild(0) // the identifier x
	if got := (urlReader{src: []byte(src), hops: maxURLHops}).inner(leaf); got != "" {
		t.Errorf("inner(leaf) = %q, want empty", got)
	}
	if got := clauseNames(leaf.Parent(), []byte(src)); got != nil {
		t.Errorf("clauseNames(statement) = %v, want nil", got)
	}
	if !anyNil(leaf, nil) || anyNil(leaf) {
		t.Error("anyNil should report a nil node and only that")
	}
}
