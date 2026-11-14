package typescript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// targetsOf analyzes rel inside root and returns its dependency targets.
func targetsOf(t *testing.T, a *Analyzer, root, rel string) map[string]bool {
	t.Helper()
	_, edges, err := a.AnalyzeRooted(root, filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return dependencyTargets(edges)
}

func requireTargets(t *testing.T, got map[string]bool, want ...string) {
	t.Helper()
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing dependency target %q; got %v", w, got)
		}
	}
}

func TestAliases_TSConfigPaths(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tsconfig.json": `{
	// comments and trailing commas are legal here
	"compilerOptions": {
		"baseUrl": ".",
		"paths": {
			"@app/*": ["src/app/*"],
			"@app/special/*": ["src/special/*"],
			"@config": ["src/config.ts"],
		},
	},
}`,
		"src/main.ts": `import { a } from '@app/a';
import { s } from '@app/special/s';
import { config } from '@config';
import { nope } from '@app/nope';
`,
		"src/app/a.ts":     "export const a = 1;\n",
		"src/special/s.ts": "export const s = 1;\n",
		"src/config.ts":    "export const config = 1;\n",
	})

	requireTargets(t, targetsOf(t, New(), root, "src/main.ts"),
		"mod:src/app/a",
		"mod:src/special/s", // the longer prefix wins over @app/*
		"mod:src/config",    // exact alias, no wildcard
		"import:@app/nope",  // alias matches, no such file
	)
}

func TestAliases_TSConfigExtends(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tsconfig.json":      `{"extends": "./config/base", "compilerOptions": {"strict": true}}`,
		"config/base.json":   `{"compilerOptions": {"paths": {"~/*": ["../src/*"]}}}`,
		"src/main.ts":        "import { a } from '~/lib/a';\n",
		"src/lib/a.ts":       "export const a = 1;\n",
		"pkg/tsconfig.json":  `{"extends": "../tsconfig.json"}`,
		"pkg/src/nested.ts":  "import { a } from '~/lib/a';\n",
		"loop/tsconfig.json": `{"extends": "./tsconfig.json"}`,
		"loop/x.ts":          "import { a } from '~/lib/a';\n",
	})
	a := New()

	// Paths resolve against the directory of the config that declares them.
	requireTargets(t, targetsOf(t, a, root, "src/main.ts"), "mod:src/lib/a")
	// A nested project inherits through a second extends hop.
	requireTargets(t, targetsOf(t, a, root, "pkg/src/nested.ts"), "mod:src/lib/a")
	// A config that extends itself terminates and resolves nothing.
	requireTargets(t, targetsOf(t, a, root, "loop/x.ts"), "import:~/lib/a")
}

func TestAliases_PackageJSONImports(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json": `{
	"imports": {
		"#lib/*": "./src/lib/*",
		"#env": {"default": "./src/env.ts"},
		"#dep": "some-package"
	}
}`,
		"src/main.ts": `import { trace } from '#lib/server/trace.ts';
import { env } from '#env';
import { dep } from '#dep';
`,
		"src/lib/server/trace.ts": "export const trace = 1;\n",
		"src/env.ts":              "export const env = 1;\n",
	})

	requireTargets(t, targetsOf(t, New(), root, "src/main.ts"),
		"mod:src/lib/server/trace",
		"mod:src/env",
		"import:#dep", // points at a package, not a source file
	)
}

func TestAliases_SvelteKitLibDefault(t *testing.T) {
	root := writeTree(t, map[string]string{
		// No .svelte-kit/tsconfig.json: a fresh checkout before `svelte-kit sync`.
		"package.json":                `{"devDependencies": {"@sveltejs/kit": "^2.0.0"}}`,
		"tsconfig.json":               `{"extends": "./.svelte-kit/tsconfig.json"}`,
		"src/routes/api/+server.ts":   "import { run } from '$lib/server/run';\nimport { shared } from '$lib';\n",
		"src/lib/server/run.ts":       "export const run = 1;\n",
		"src/lib/index.ts":            "export const shared = 1;\n",
		"plain/package.json":          `{"dependencies": {"express": "^4.0.0"}}`,
		"plain/src/main.ts":           "import { run } from '$lib/server/run';\n",
		"plain/src/lib/server/run.ts": "export const run = 1;\n",
	})
	a := New()

	requireTargets(t, targetsOf(t, a, root, "src/routes/api/+server.ts"),
		"mod:src/lib/server/run",
		"mod:src/lib/index",
	)
	// Without SvelteKit as a dependency, $lib means nothing.
	requireTargets(t, targetsOf(t, a, root, "plain/src/main.ts"), "import:$lib/server/run")
}

func TestAliases_ConfigChangeIsPickedUp(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tsconfig.json": `{"compilerOptions": {"paths": {"@x/*": ["old/*"]}}}`,
		"main.ts":       "import { a } from '@x/a';\n",
		"old/a.ts":      "export const a = 1;\n",
		"new/a.ts":      "export const a = 2;\n",
	})
	a := New()
	requireTargets(t, targetsOf(t, a, root, "main.ts"), "mod:old/a")

	cfg := filepath.Join(root, "tsconfig.json")
	if err := os.WriteFile(cfg, []byte(`{"compilerOptions": {"paths": {"@x/*": ["new/*"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Same size as before, so move the modification time explicitly.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(cfg, later, later); err != nil {
		t.Fatal(err)
	}
	requireTargets(t, targetsOf(t, a, root, "main.ts"), "mod:new/a")
}
