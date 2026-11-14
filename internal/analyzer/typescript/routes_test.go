package typescript

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

// endpointNames returns the names of the endpoint nodes, sorted.
func endpointNames(nodes []*model.Node) []string {
	var names []string
	for _, n := range nodes {
		if n.Type == model.NodeEndpoint {
			names = append(names, n.Name)
		}
	}
	sort.Strings(names)
	return names
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

func TestRouteCall_IsRouteRegistration(t *testing.T) {
	tests := []struct {
		name string
		call routeCall
		want bool
	}{
		{"router with path and handler", routeCall{receiver: "router", route: "/users", argCount: 2}, true},
		{"unknown receiver, path, handler", routeCall{receiver: "users", route: "/users", argCount: 2}, true},
		{"unknown receiver in an express file", routeCall{receiver: "users", route: "/users", argCount: 1, inWebApp: true}, true},
		{"router with a relative route", routeCall{receiver: "router", route: "users", argCount: 2}, true},
		{"wildcard route", routeCall{receiver: "app", route: "*", argCount: 2}, true},
		{"form field read", routeCall{receiver: "form", route: "file", argCount: 1}, false},
		{"header read", routeCall{receiver: "headers", route: "content-type", argCount: 1}, false},
		{"express setting read", routeCall{receiver: "app", route: "env", argCount: 1}, false},
		{"cache read keyed by a path", routeCall{receiver: "cache", route: "/api/users", argCount: 1}, false},
		{"axios call", routeCall{receiver: "axios", route: "/users", argCount: 2}, false},
		{"router named api in an express file", routeCall{receiver: "api", route: "/users", argCount: 2, inWebApp: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.call.isRouteRegistration(); got != tt.want {
				t.Fatalf("isRouteRegistration() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The six false endpoints bead 4s8 reported on ariadne, in one file.
func TestAnalyze_FieldReadsAreNotEndpoints(t *testing.T) {
	path := writeTSFile(t, t.TempDir(), "input.ts", `export async function read(request: Request) {
  const form = await request.formData();
  const file = form.get('file');
  const url = form.get('url');
  const type = request.headers.get('content-type');
  const auth = request.headers.get('authorization');
  return { file, url, type, auth, q: new URL(request.url).searchParams.get('q') };
}
`)
	nodes, _, err := New().Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	if got := endpointNames(nodes); len(got) != 0 {
		t.Fatalf("expected no endpoints, got %v", got)
	}
}

func TestAnalyzeRooted_EndpointIDsAreRootRelative(t *testing.T) {
	const src = "import express from 'express';\nconst router = express.Router();\nrouter.get('/list', (req, res) => res.send('ok'));\n"
	root := writeTree(t, map[string]string{
		"src/users/routes.ts":  src,
		"src/orders/routes.ts": src,
	})
	a := New()
	ids := make(map[string]bool)
	for _, rel := range []string{"src/users/routes.ts", "src/orders/routes.ts"} {
		nodes, _, err := a.AnalyzeRooted(root, filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("AnalyzeRooted failed: %v", err)
		}
		for _, n := range nodes {
			if n.Type == model.NodeEndpoint {
				ids[n.ID] = true
			}
		}
	}
	for _, want := range []string{"endpoint:src/users/routes.ts:3", "endpoint:src/orders/routes.ts:3"} {
		if !ids[want] {
			t.Fatalf("missing endpoint ID %q; got %v", want, ids)
		}
	}
}

func TestAnalyzeRooted_SvelteKitServerRoutes(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/routes/api/analyze/+server.ts": `import type { RequestHandler } from './$types';
export const POST: RequestHandler = async ({ request }) => new Response();
export async function GET() { return new Response(); }
const remove: RequestHandler = async () => new Response();
export { remove as DELETE };
export const prerender = false;
`,
		"src/routes/(app)/items/[id]/+server.ts": "export const GET = () => new Response();\n",
		"src/routes/+server.ts":                  "export const GET = () => new Response();\n",
		"src/lib/+server.ts":                     "export const GET = () => new Response();\n",
	})
	tests := []struct {
		file string
		want []string
	}{
		{"src/routes/api/analyze/+server.ts", []string{"DELETE /api/analyze", "GET /api/analyze", "POST /api/analyze"}},
		{"src/routes/(app)/items/[id]/+server.ts", []string{"GET /items/[id]"}},
		{"src/routes/+server.ts", []string{"GET /"}},
		{"src/lib/+server.ts", nil}, // not under a routes folder
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

func TestAnalyzeRooted_SvelteKitFormActions(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/routes/cases/[id]/+page.server.ts": `import type { Actions } from './$types';
const pin = async () => ({});
export const load = async () => ({});
export const actions = {
	default: async () => ({}),
	pin,
	async review() { return {}; },
	'assign': async () => ({})
} satisfies Actions;
`,
	})
	file := filepath.Join(root, filepath.FromSlash("src/routes/cases/[id]/+page.server.ts"))
	nodes, _, err := New().AnalyzeRooted(root, file)
	if err != nil {
		t.Fatalf("AnalyzeRooted failed: %v", err)
	}
	want := []string{"POST /cases/[id]", "POST /cases/[id]?/assign", "POST /cases/[id]?/pin", "POST /cases/[id]?/review"}
	if got := endpointNames(nodes); !equalStrings(got, want) {
		t.Fatalf("endpoints = %v, want %v", got, want)
	}
	for _, n := range nodes {
		if n.Type == model.NodeEndpoint && n.Properties["route"] != "/cases/[id]" {
			t.Fatalf("route property = %q, want the page path without the action", n.Properties["route"])
		}
	}
}

func TestAnalyzeRooted_SameOriginCallsTargetRoutes(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/lib/client.ts": "export async function load(id: string) {\n" +
			"  await fetch('/api/items?limit=5');\n" +
			"  await fetch(`/api/items/${id}`, { method: 'DELETE' });\n" +
			"  await fetch('https://example.com/v1/things');\n" +
			"  await fetch(`https://${id}.example.com/v1`);\n" +
			"  await fetch(id);\n" +
			"}\n",
	})
	_, edges, err := New().AnalyzeRooted(root, filepath.Join(root, "src", "lib", "client.ts"))
	if err != nil {
		t.Fatalf("AnalyzeRooted failed: %v", err)
	}
	got := make(map[string]bool)
	for _, e := range edges {
		if e.Type == model.EdgeAPICall {
			got[e.Target] = true
		}
	}
	want := []string{"route:GET /api/items", "route:DELETE /api/items/*", "service:example.com", "service:x.example.com"}
	if len(got) != len(want) {
		t.Fatalf("api call targets = %v, want %v", got, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("missing api call target %q; got %v", w, got)
		}
	}
}
