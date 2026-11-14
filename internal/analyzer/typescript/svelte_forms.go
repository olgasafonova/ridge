package typescript

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// SvelteKit forms name their form action in markup, not in script:
//
//	<form method="POST" action="?/assign">
//	<button formaction="?/review">
//	<form method="POST" action="/login?/reset">
//
// "?/name" alone posts to the page the component is rendered on; with a path
// in front it posts to that page. Each becomes an edge to a form-action
// placeholder that the graph resolves to the +page.server.ts endpoint.

// formActionAttr matches a quoted action or formaction attribute.
var formActionAttr = regexp.MustCompile(`(?:^|\s)(?:form)?action\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// actionName is the form-action name at the start of what follows "?/".
var actionName = regexp.MustCompile(`^[A-Za-z_$][\w$-]*`)

// svelteFormActionEdges returns one edge per distinct form action named in
// the markup of a Svelte component.
func svelteFormActionEdges(src []byte, modID string) []*model.Edge {
	scripts := scriptRanges(src)
	seen := make(map[string]bool)
	var edges []*model.Edge
	for _, m := range formActionAttr.FindAllSubmatchIndex(src, -1) {
		start, end := m[2], m[3]
		if start < 0 {
			start, end = m[4], m[5]
		}
		if scripts.contain(start) {
			continue
		}
		value := string(src[start:end])
		path, name, ok := parseFormAction(value)
		target := model.ActionTarget(path, name)
		if !ok || seen[target] {
			continue
		}
		seen[target] = true
		edges = append(edges, &model.Edge{
			Source:     modID,
			Target:     target,
			Type:       model.EdgeAPICall,
			Label:      "POST " + path + "?/" + name,
			Confidence: 0.7,
			Properties: map[string]string{
				"url":  value,
				"line": fmt.Sprintf("%d", 1+bytes.Count(src[:start], []byte{'\n'})),
			},
		})
	}
	return edges
}

// parseFormAction splits an action attribute value into the page path and
// the form-action name. ok is false for a value that names no form action,
// one built at run time ("?/{name}"), or one relative to the current URL
// ("edit?/save"), whose page cannot be told from the file.
func parseFormAction(value string) (path, name string, ok bool) {
	if strings.Contains(value, "{") {
		return "", "", false
	}
	path, rest, found := strings.Cut(value, "?/")
	if !found || !namesPage(path) {
		return "", "", false
	}
	name = actionName.FindString(rest)
	return path, name, name != ""
}

// namesPage reports whether what precedes "?/" says which page is posted to:
// nothing (the current page) or a path on the app's own origin.
func namesPage(path string) bool {
	return path == "" || isSameOriginPath(path)
}

// byteRanges is a list of [start, end) offsets into a file.
type byteRanges [][2]int

func (r byteRanges) contain(offset int) bool {
	for _, rng := range r {
		if offset >= rng[0] && offset < rng[1] {
			return true
		}
	}
	return false
}

// scriptRanges returns where the <script> blocks of a component are.
func scriptRanges(src []byte) byteRanges {
	var out byteRanges
	pos := 0
	for {
		start, end, ok := nextScriptBody(src, pos)
		if !ok {
			return out
		}
		out = append(out, [2]int{start, end})
		pos = end
	}
}
