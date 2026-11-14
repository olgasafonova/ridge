package typescript

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// SvelteKit forms name their form action in markup, not in script:
//
//	<form method="POST" action="?/assign">
//	<button formaction="?/review">
//	<form method="POST" action="/login?/reset">
//	<form method="POST" action={pinned ? '?/unpin' : '?/pin'}>
//	<form method="POST">
//
// "?/name" alone posts to the page the component is rendered on; with a path
// in front it posts to that page. An expression in braces names every string
// literal it may evaluate to, as long as nothing in it builds a string at run
// time. A POST form with no action attribute posts to the page's default
// action, unless the component sets formaction anywhere: buttons may then
// submit that form to named actions instead, as in
//
//	<form id="row-1" method="POST">...</form>
//	<button form="row-1" formaction="?/review">
//
// and the file does not say whether anything posts to the default. Each becomes an edge to a form-action placeholder that the graph
// resolves to the +page.server.ts endpoint.

// formActionAttr matches a quoted action or formaction attribute.
var formActionAttr = regexp.MustCompile(`(?:^|\s)(?:form)?action\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// formActionExpr matches an action or formaction attribute given as an
// expression in braces, without nested braces.
var formActionExpr = regexp.MustCompile(`(?:^|\s)(?:form)?action\s*=\s*\{([^{}]*)\}`)

// quotedLiteral matches a single- or double-quoted string literal.
var quotedLiteral = regexp.MustCompile(`"([^"]*)"|'([^']*)'`)

// formTag matches an opening <form> tag and captures its attributes.
var formTag = regexp.MustCompile(`<form\b([^>]*)>`)

// postMethod matches method="POST" in any case, quoted or not.
var postMethod = regexp.MustCompile(`(?i)(?:^|\s)method\s*=\s*["']?post\b`)

// anyActionAttr matches an action attribute of any form.
var anyActionAttr = regexp.MustCompile(`(?:^|\s)action\s*=`)

// anyFormactionAttr matches a formaction attribute of any form.
var anyFormactionAttr = regexp.MustCompile(`(?:^|\s)formaction\s*=`)

// actionName is the form-action name at the start of what follows "?/".
var actionName = regexp.MustCompile(`^[A-Za-z_$][\w$-]*`)

// defaultActionName is the name SvelteKit gives the action a form without an
// action attribute posts to.
const defaultActionName = "default"

// actionUse is a form action named in markup: the attribute text that names
// it and where that text starts.
type actionUse struct {
	value string
	start int
	path  string
	name  string
}

// svelteFormActionEdges returns one edge per distinct form action named in
// the markup of a Svelte component.
func svelteFormActionEdges(src []byte, modID string) []*model.Edge {
	scripts := scriptRanges(src)
	seen := make(map[string]bool)
	var edges []*model.Edge
	for _, use := range actionUses(src) {
		target := model.ActionTarget(use.path, use.name)
		if scripts.contain(use.start) || seen[target] {
			continue
		}
		seen[target] = true
		edges = append(edges, &model.Edge{
			Source:     modID,
			Target:     target,
			Type:       model.EdgeAPICall,
			Label:      "POST " + use.path + "?/" + use.name,
			Confidence: 0.7,
			Properties: map[string]string{
				"url":  use.value,
				"line": fmt.Sprintf("%d", 1+bytes.Count(src[:use.start], []byte{'\n'})),
			},
		})
	}
	return edges
}

// actionUses lists the form actions the markup names, in the order they
// appear: quoted attributes, attributes given as expressions, and POST forms
// with no action.
func actionUses(src []byte) []actionUse {
	uses := quotedActionUses(src)
	uses = append(uses, exprActionUses(src)...)
	uses = append(uses, defaultActionUses(src)...)
	sort.SliceStable(uses, func(i, j int) bool { return uses[i].start < uses[j].start })
	return uses
}

// quotedActionUses returns the form actions named by quoted attributes.
func quotedActionUses(src []byte) []actionUse {
	var uses []actionUse
	for _, m := range formActionAttr.FindAllSubmatchIndex(src, -1) {
		start, end := quotedGroup(m)
		uses = appendParsed(uses, string(src[start:end]), start)
	}
	return uses
}

// exprActionUses returns the form actions named by attributes given as
// expressions in braces.
func exprActionUses(src []byte) []actionUse {
	var uses []actionUse
	for _, m := range formActionExpr.FindAllSubmatchIndex(src, -1) {
		for _, lit := range exprLiterals(src[m[2]:m[3]]) {
			uses = appendParsed(uses, lit.value, m[2]+lit.start)
		}
	}
	return uses
}

// quotedGroup returns the bounds of whichever of a match's two groups, the
// double- or the single-quoted one, matched.
func quotedGroup(m []int) (start, end int) {
	if m[2] >= 0 {
		return m[2], m[3]
	}
	return m[4], m[5]
}

// defaultActionUses returns the POST forms without an action attribute, or
// none when the component sets formaction anywhere.
func defaultActionUses(src []byte) []actionUse {
	if anyFormactionAttr.Match(src) {
		return nil
	}
	var uses []actionUse
	for _, m := range formTag.FindAllSubmatchIndex(src, -1) {
		attrs := src[m[2]:m[3]]
		if postMethod.Match(attrs) && !anyActionAttr.Match(attrs) {
			uses = append(uses, actionUse{start: m[0], name: defaultActionName})
		}
	}
	return uses
}

// appendParsed adds value to uses when it names a form action.
func appendParsed(uses []actionUse, value string, start int) []actionUse {
	path, name, ok := parseFormAction(value)
	if !ok {
		return uses
	}
	return append(uses, actionUse{value: value, start: start, path: path, name: name})
}

// exprLiterals returns the string literals an attribute expression may
// evaluate to, or none when it may also build a string at run time: a
// template literal or a "+" between parts.
func exprLiterals(expr []byte) []actionUse {
	rest := quotedLiteral.ReplaceAll(expr, nil)
	if bytes.ContainsAny(rest, "`+") {
		return nil
	}
	var out []actionUse
	for _, m := range quotedLiteral.FindAllSubmatchIndex(expr, -1) {
		start, end := quotedGroup(m)
		out = append(out, actionUse{value: string(expr[start:end]), start: start})
	}
	return out
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
