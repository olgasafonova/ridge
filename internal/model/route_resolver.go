package model

import (
	"sort"
	"strings"
)

// A call to a path on the app's own origin (fetch('/api/analyze')) is emitted
// by analyzers as an edge to a "route:<METHOD> <path>" placeholder, because
// the analyzer sees one file at a time and the endpoint serving that path is
// declared in another. ResolvedEdges rewrites the placeholder to the endpoint
// node whose route matches.

// routeTargetPrefix marks an edge target that names a route, not a node.
const routeTargetPrefix = "route:"

// RouteWildcard stands for a part of a called path that is only known at run
// time, such as the ${id} in fetch(`/api/items/${id}`).
const RouteWildcard = "*"

// RouteTarget builds the placeholder edge target for a call to path with the
// given HTTP method. Any query string or fragment is dropped.
func RouteTarget(method, path string) string {
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	return routeTargetPrefix + strings.ToUpper(method) + " " + path
}

// routeResolver maps route placeholders to endpoint node IDs.
type routeResolver struct {
	endpoints []routeEndpoint
	cache     map[string]string // "" = no endpoint serves it
}

type routeEndpoint struct {
	id       string
	method   string
	segments []string
}

func newRouteResolver(nodes map[string]*Node) *routeResolver {
	r := &routeResolver{cache: make(map[string]string)}
	for _, n := range nodes {
		route := n.Properties["route"]
		if n.Type != NodeEndpoint || !strings.HasPrefix(route, "/") {
			continue
		}
		r.endpoints = append(r.endpoints, routeEndpoint{
			id:       n.ID,
			method:   strings.ToUpper(n.Properties["method"]),
			segments: routeSegments(route),
		})
	}
	// Map iteration order is random; ties below go to the lowest ID.
	sort.Slice(r.endpoints, func(i, j int) bool { return r.endpoints[i].id < r.endpoints[j].id })
	return r
}

// resolve returns the endpoint ID for a route placeholder. Targets without
// the route prefix pass through unchanged.
func (r *routeResolver) resolve(target string) (string, bool) {
	spec, ok := strings.CutPrefix(target, routeTargetPrefix)
	if !ok {
		return target, true
	}
	if id, cached := r.cache[target]; cached {
		return id, id != ""
	}
	method, path, _ := strings.Cut(spec, " ")
	id := r.best(method, routeSegments(path))
	r.cache[target] = id
	return id, id != ""
}

// best picks the endpoint whose route matches the called path. The route with
// the most literal segments wins ("/api/trace" over "/api/[id]"), then one
// with the same HTTP method. The method only breaks ties: a caller's method
// is often not visible where the URL is.
func (r *routeResolver) best(method string, called []string) string {
	bestID, bestScore := "", -1
	for _, ep := range r.endpoints {
		literal, ok := matchRoute(ep.segments, called)
		if !ok {
			continue
		}
		score := literal * 2
		if ep.method == method {
			score++
		}
		if score > bestScore {
			bestID, bestScore = ep.id, score
		}
	}
	return bestID
}

func routeSegments(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

// matchRoute reports whether a declared route serves a called path, and how
// many segments matched literally.
func matchRoute(declared, called []string) (literal int, ok bool) {
	for i, seg := range declared {
		if isRestSegment(seg) {
			return literal, true
		}
		if i >= len(called) {
			return 0, false
		}
		switch {
		case isParamSegment(seg) || strings.Contains(called[i], RouteWildcard):
		case seg == called[i]:
			literal++
		default:
			return 0, false
		}
	}
	return literal, len(called) == len(declared)
}

// isParamSegment recognizes a one-segment parameter in the spellings the
// analyzers keep: ":id" (Express), "[id]" (SvelteKit), "{id}" (Go, Spring),
// "<id>" (Flask).
func isParamSegment(seg string) bool {
	if seg == "" {
		return false
	}
	switch seg[0] {
	case ':', '[', '{', '<':
		return true
	}
	return false
}

// isRestSegment recognizes a parameter that takes the rest of the path:
// "*", "[...rest]" (SvelteKit), "{rest...}" (Go).
func isRestSegment(seg string) bool {
	return seg == "*" || strings.HasPrefix(seg, "[...") || strings.HasSuffix(seg, "...}")
}
