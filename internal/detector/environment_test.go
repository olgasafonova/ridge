package detector

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile (t, dir, name, content) is shared from boundary_test.go.

func rulesOf(violations []Violation) map[string]int {
	out := map[string]int{}
	for _, v := range violations {
		out[v.Rule]++
	}
	return out
}

func TestCheckEnvironment_NonexistentRoot(t *testing.T) {
	if got := CheckEnvironment(filepath.Join(os.TempDir(), "ridge-does-not-exist")); got != nil {
		t.Fatalf("expected nil for nonexistent root, got %+v", got)
	}
	if got := CheckEnvironment(""); got != nil {
		t.Fatalf("expected nil for empty root, got %+v", got)
	}
}

func TestCheckEnvironment_GoModReplaceMissing(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "go.mod", `module example.com/app

go 1.22

replace example.com/lib => ../missing-lib
`)

	violations := CheckEnvironment(repo)

	if rulesOf(violations)["go_mod_replace_target_missing"] != 1 {
		t.Fatalf("expected 1 go_mod_replace_target_missing, got %+v", violations)
	}
}

func TestCheckEnvironment_GoModReplaceExists(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "go.mod", `module example.com/app

replace example.com/lib => ./lib
`)

	if violations := CheckEnvironment(repo); len(violations) != 0 {
		t.Fatalf("expected no violations for existing replace target, got %+v", violations)
	}
}

func TestCheckEnvironment_GoModReplaceBlockForm(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "present"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "go.mod", `module example.com/app

replace (
	example.com/ok => ./present
	example.com/broken v1.2.3 => ./absent // versioned LHS, comment
	example.com/proxy => example.com/other v1.0.0
)
`)

	violations := CheckEnvironment(repo)

	counts := rulesOf(violations)
	if counts["go_mod_replace_target_missing"] != 1 {
		t.Fatalf("expected exactly 1 missing-target violation (./absent), got %+v", violations)
	}
}

func TestCheckEnvironment_TSConfigPathsMissing(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "src", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	// JSONC on purpose: comments + trailing comma, both legal in tsconfig.
	writeFile(t, repo, "tsconfig.json", `{
  // path aliases
  "compilerOptions": {
    "paths": {
      "@lib/*": ["src/lib/*"],
      "@ghost/*": ["src/ghost/*"], /* does not exist */
    },
  },
}`)

	violations := CheckEnvironment(repo)

	counts := rulesOf(violations)
	if counts["tsconfig_path_target_missing"] != 1 {
		t.Fatalf("expected exactly 1 tsconfig_path_target_missing (@ghost), got %+v", violations)
	}
}

func TestCheckEnvironment_TSConfigBaseURLMissing(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "tsconfig.json", `{
  "compilerOptions": {
    "baseUrl": "./app",
    "paths": { "@x/*": ["x/*"] }
  }
}`)

	violations := CheckEnvironment(repo)

	counts := rulesOf(violations)
	if counts["tsconfig_baseurl_missing"] != 1 {
		t.Fatalf("expected 1 tsconfig_baseurl_missing, got %+v", violations)
	}
	// paths violations would be N duplicates of the baseUrl root cause.
	if counts["tsconfig_path_target_missing"] != 0 {
		t.Fatalf("expected no path violations when baseUrl is the root cause, got %+v", violations)
	}
}

func TestCheckEnvironment_SkipsNodeModules(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, filepath.Join("node_modules", "dep", "go.mod"), `module dep

replace x => ./gone
`)

	if violations := CheckEnvironment(repo); len(violations) != 0 {
		t.Fatalf("expected node_modules to be skipped, got %+v", violations)
	}
}

func TestStripJSONC_PreservesStrings(t *testing.T) {
	in := `{"a": "http://example.com//not-a-comment", "b": "/*keep*/",}`
	want := `{"a": "http://example.com//not-a-comment", "b": "/*keep*/"}`
	if got := string(stripJSONC([]byte(in))); got != want {
		t.Fatalf("stripJSONC mangled strings:\n got: %s\nwant: %s", got, want)
	}
}
