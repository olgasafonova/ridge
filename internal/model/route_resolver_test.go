package model

import "testing"

func endpointNode(id, method, route string) *Node {
	return &Node{ID: id, Type: NodeEndpoint, Properties: map[string]string{"method": method, "route": route}}
}

func TestRouteTarget(t *testing.T) {
	if got := RouteTarget("post", "/api/items?limit=5#top"); got != "route:POST /api/items" {
		t.Fatalf("RouteTarget = %q", got)
	}
}

func TestResolvedEdges_RouteTargets(t *testing.T) {
	g := NewGraph("/tmp/project")
	g.AddNode(endpointNode("endpoint:trace:1", "post", "/api/trace"))
	g.AddNode(endpointNode("endpoint:items:1", "get", "/api/items"))
	g.AddNode(endpointNode("endpoint:items:9", "post", "/api/items"))
	g.AddNode(endpointNode("endpoint:item:1", "get", "/api/[id]"))
	g.AddNode(endpointNode("endpoint:files:1", "get", "/files/[...rest]"))
	g.AddNode(endpointNode("endpoint:express:1", "get", "/users/:id"))

	tests := []struct {
		target string
		want   string // "" = dropped
	}{
		{"route:POST /api/trace", "endpoint:trace:1"},
		{"route:GET /api/trace", "endpoint:trace:1"}, // literal route beats /api/[id]; method only breaks ties
		{"route:POST /api/items", "endpoint:items:9"},
		{"route:GET /api/items/", "endpoint:items:1"},
		{"route:GET /api/42", "endpoint:item:1"},
		{"route:GET /api/*", "endpoint:item:1"},
		{"route:GET /files/a/b/c.png", "endpoint:files:1"},
		{"route:GET /users/*", "endpoint:express:1"},
		{"route:GET /nowhere", ""},
		{"route:GET /api/items/1/extra", ""},
	}
	// One caller per case: ResolvedEdges keeps a single edge per
	// source/target pair, and several cases resolve to the same endpoint.
	for _, tt := range tests {
		caller := "mod:caller of " + tt.target
		g.AddNode(&Node{ID: caller, Type: NodeModule})
		g.AddEdge(&Edge{Source: caller, Target: tt.target, Type: EdgeAPICall, Label: tt.target})
	}
	got := make(map[string]string)
	for _, e := range g.ResolvedEdges() {
		got[e.Label] = e.Target
	}
	for _, tt := range tests {
		if got[tt.target] != tt.want {
			t.Errorf("%s resolved to %q, want %q", tt.target, got[tt.target], tt.want)
		}
	}
}

func TestResolvedEdges_RouteTargetEdgeCases(t *testing.T) {
	g := NewGraph("/tmp/project")
	g.AddNode(endpointNode("endpoint:home:1", "get", "/"))
	g.AddNode(endpointNode("endpoint:trace:1", "post", "/api/trace"))
	g.AddNode(endpointNode("endpoint:double:1", "get", "/a//b"))

	tests := []struct {
		caller string
		target string
		want   string // "" = dropped
	}{
		{"mod:home", "route:GET /", "endpoint:home:1"},              // a route with no segments
		{"mod:short", "route:GET /api", ""},                         // called path is shorter than the declared route
		{"mod:double", "route:GET /a//b", "endpoint:double:1"},      // an empty segment is a literal, not a parameter
		{"mod:first", "route:POST /api/trace", "endpoint:trace:1"},  // first lookup fills the cache
		{"mod:second", "route:POST /api/trace", "endpoint:trace:1"}, // second caller is served from it
		{"mod:miss1", "route:GET /nowhere", ""},
		{"mod:miss2", "route:GET /nowhere", ""}, // a cached miss stays a miss
	}
	for _, tt := range tests {
		g.AddNode(&Node{ID: tt.caller, Type: NodeModule})
		g.AddEdge(&Edge{Source: tt.caller, Target: tt.target, Type: EdgeAPICall})
	}
	got := make(map[string]string)
	for _, e := range g.ResolvedEdges() {
		got[e.Source] = e.Target
	}
	for _, tt := range tests {
		if got[tt.caller] != tt.want {
			t.Errorf("%s -> %s resolved to %q, want %q", tt.caller, tt.target, got[tt.caller], tt.want)
		}
	}
}
