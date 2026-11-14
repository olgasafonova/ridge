package typescript

import (
	"path/filepath"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func TestParseFormAction(t *testing.T) {
	tests := []struct {
		value, path, name string
		ok                bool
	}{
		{"?/assign", "", "assign", true},
		{"?/revise&tab=rules", "", "revise", true},
		{"/login?/reset", "/login", "reset", true},
		{"/cases/42?/set-priority", "/cases/42", "set-priority", true},
		{"/dashboard", "", "", false},        // a page, not a form action
		{"?/{name}", "", "", false},          // built at run time
		{"edit?/save", "", "", false},        // relative to the current URL
		{"//evil.example?/x", "", "", false}, // another origin
		{"?/", "", "", false},
		{"?/9lives", "", "", false},
	}
	for _, tt := range tests {
		path, name, ok := parseFormAction(tt.value)
		if ok != tt.ok || (ok && (path != tt.path || name != tt.name)) {
			t.Errorf("parseFormAction(%q) = %q, %q, %v; want %q, %q, %v", tt.value, path, name, ok, tt.path, tt.name, tt.ok)
		}
	}
}

func TestAnalyzeRooted_SvelteFormActions(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/lib/CaseBar.svelte": "<script lang=\"ts\">\n" +
			"\tconst hint = ' action=\"?/inScript\"';\n" +
			"</script>\n" +
			"<form method=\"POST\" action=\"?/assign\" use:enhance>\n" +
			"\t<button formaction='?/review'>Review</button>\n" +
			"\t<button formaction=\"?/review\">Again</button>\n" +
			"</form>\n" +
			"<form method=\"POST\"\n" +
			"\taction=\"/login?/reset\">\n" +
			"</form>\n" +
			"<form method=\"GET\" action=\"/dashboard\"></form>\n" +
			"<form action={dynamic}></form>\n" +
			"<div data-action=\"?/notAnAttribute\"></div>\n",
	})
	_, edges, err := New().AnalyzeRooted(root, filepath.Join(root, "src", "lib", "CaseBar.svelte"))
	if err != nil {
		t.Fatalf("AnalyzeRooted failed: %v", err)
	}

	lines := make(map[string]string)
	for _, e := range edges {
		if e.Type != model.EdgeAPICall {
			continue
		}
		if e.Source != "mod:src/lib/CaseBar.svelte" {
			t.Errorf("edge source = %q", e.Source)
		}
		lines[e.Target] = e.Properties["line"]
	}
	want := map[string]string{
		"action:?/assign":      "4",
		"action:?/review":      "5", // named twice, one edge, first line
		"action:/login?/reset": "9",
	}
	if len(lines) != len(want) {
		t.Fatalf("form action edges = %v, want %v", lines, want)
	}
	for target, line := range want {
		if lines[target] != line {
			t.Errorf("%s on line %q, want %q", target, lines[target], line)
		}
	}
}

func TestSvelteFormActionEdges_NoMarkup(t *testing.T) {
	for _, src := range []string{"", "<script>\nconst a = 'action=\"?/x\"';\n</script>\n", "<p>action</p>"} {
		if edges := svelteFormActionEdges([]byte(src), "mod:x"); len(edges) != 0 {
			t.Errorf("svelteFormActionEdges(%q) = %d edges, want none", src, len(edges))
		}
	}
}
