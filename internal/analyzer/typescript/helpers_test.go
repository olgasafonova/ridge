package typescript

import (
	"context"
	"path/filepath"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/olgasafonova/ridge/internal/model"
)

// parseTS parses TypeScript source and returns the root node.
func parseTS(t *testing.T, src string) *sitter.Node {
	t.Helper()
	tree, err := New().parserFor("x.ts").ParseCtx(context.Background(), nil, []byte(src))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	t.Cleanup(tree.Close)
	return tree.RootNode()
}

// apiCallTargets returns the targets of the API-call edges.
func apiCallTargets(edges []*model.Edge) map[string]bool {
	out := make(map[string]bool)
	for _, e := range edges {
		if e.Type == model.EdgeAPICall {
			out[e.Target] = true
		}
	}
	return out
}

// The call helpers are handed call expressions by the analyzer. They must
// also give an empty answer, not a panic, for a node with no arguments or
// for a missing node.
func TestCallHelpers_NodesWithoutArguments(t *testing.T) {
	src := []byte("const x = 1;\n")
	root := parseTS(t, string(src))

	if got := receiverName(nil, src); got != "" {
		t.Errorf("receiverName(nil) = %q, want empty", got)
	}
	if got := propertyName(nil, src); got != "" {
		t.Errorf("propertyName(nil) = %q, want empty", got)
	}
	if got := argCount(root); got != 0 {
		t.Errorf("argCount = %d, want 0", got)
	}
	if arg := firstArg(root); arg != nil {
		t.Errorf("firstArg = %v, want nil", arg)
	}
	if s, ok := firstURLArg(root, src); ok || s != "" {
		t.Errorf("firstURLArg = %q, %v; want empty, false", s, ok)
	}
	if s, ok := firstStringArg(root, src); ok || s != "" {
		t.Errorf("firstStringArg = %q, %v; want empty, false", s, ok)
	}
}

// A route registered on a call result (getRouter().get) has no receiver name;
// the path and the handler still make it a route.
func TestAnalyze_RouteOnCallResult(t *testing.T) {
	path := writeTSFile(t, t.TempDir(), "routes.ts", `import { getRouter } from './router';
getRouter().get('/health', (req, res) => res.send('ok'));
getRouter().get('name');
`)
	nodes, _, err := New().Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	if got, want := endpointNames(nodes), []string{"GET /health"}; !equalStrings(got, want) {
		t.Fatalf("endpoints = %v, want %v", got, want)
	}
}

func TestAnalyzeRooted_FetchMethodFromOptions(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/lib/client.ts": `export async function run(opts: RequestInit, base: RequestInit) {
  await fetch('/api/a', opts);
  await fetch('/api/b', { headers: {} });
  await fetch('/api/c', { ...base, headers: {}, 'method': 'put' });
  await fetch('/api/d', { method: verb() });
}
`,
	})
	_, edges, err := New().AnalyzeRooted(root, filepath.Join(root, "src", "lib", "client.ts"))
	if err != nil {
		t.Fatalf("AnalyzeRooted failed: %v", err)
	}
	got := apiCallTargets(edges)
	want := []string{
		"route:GET /api/a", // options passed as a variable
		"route:GET /api/b", // options without a method
		"route:PUT /api/c", // method after a spread and another key
		"route:GET /api/d", // method that is not a string literal
	}
	if len(got) != len(want) {
		t.Fatalf("api call targets = %v, want %v", got, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing api call target %q; got %v", w, got)
		}
	}
}

func TestAnalyzeRooted_SvelteKitExportForms(t *testing.T) {
	root := writeTree(t, map[string]string{
		// A plain "export { GET }", next to exports that name no handler.
		"src/routes/api/plain/+server.ts": `const GET = () => new Response();
export { GET };
export declare const version: string;
export default function () { return new Response(); }
`,
		// actions built by a call: the action names are not in this file.
		"src/routes/built/+page.server.ts": "export const actions = makeActions();\n",
		// actions declared without a value.
		"src/routes/empty/+page.server.ts": "export let actions;\n",
		// a spread and a computed key have no static name; the rest count.
		"src/routes/mixed/+page.server.ts": `export const actions = ({
	...shared,
	[dynamic]: async () => ({}),
	async [alsoDynamic]() { return {}; },
	save: async () => ({})
});
`,
	})
	tests := []struct {
		file string
		want []string
	}{
		{"src/routes/api/plain/+server.ts", []string{"GET /api/plain"}},
		{"src/routes/built/+page.server.ts", nil},
		{"src/routes/empty/+page.server.ts", nil},
		{"src/routes/mixed/+page.server.ts", []string{"POST /mixed?/save"}},
	}
	a := New()
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			nodes, _, err := a.AnalyzeRooted(root, filepath.Join(root, filepath.FromSlash(tt.file)))
			if err != nil {
				t.Fatalf("AnalyzeRooted failed: %v", err)
			}
			if got := endpointNames(nodes); !equalStrings(got, tt.want) {
				t.Fatalf("endpoints = %v, want %v", got, tt.want)
			}
		})
	}
}
