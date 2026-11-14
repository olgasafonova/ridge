package typescript

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

// writeTree creates files (relative path -> content) under a fresh temp root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func moduleNode(t *testing.T, nodes []*model.Node) *model.Node {
	t.Helper()
	for _, n := range nodes {
		if n.Type == model.NodeModule {
			return n
		}
	}
	t.Fatal("no module node")
	return nil
}

func dependencyTargets(edges []*model.Edge) map[string]bool {
	targets := make(map[string]bool)
	for _, e := range edges {
		if e.Type == model.EdgeDependency {
			targets[e.Target] = true
		}
	}
	return targets
}

func TestAnalyzeRooted_SameNamedFilesGetDistinctIDs(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/ai/ports/outbound/index.ts":         "export const a = 1;\n",
		"src/assessment/ports/outbound/index.ts": "export const b = 2;\n",
	})
	a := New()

	aiNodes, _, err := a.AnalyzeRooted(root, filepath.Join(root, "src/ai/ports/outbound/index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	asNodes, _, err := a.AnalyzeRooted(root, filepath.Join(root, "src/assessment/ports/outbound/index.ts"))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := moduleNode(t, aiNodes).ID, "mod:src/ai/ports/outbound/index"; got != want {
		t.Errorf("ai module ID = %q, want %q", got, want)
	}
	if got, want := moduleNode(t, asNodes).ID, "mod:src/assessment/ports/outbound/index"; got != want {
		t.Errorf("assessment module ID = %q, want %q", got, want)
	}
}

func TestAnalyze_WithoutRootKeepsParentFolderID(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/outbound/index.ts": "import { x } from './x';\n",
		"src/outbound/x.ts":     "export const x = 1;\n",
	})

	nodes, edges, err := New().Analyze(filepath.Join(root, "src/outbound/index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := moduleNode(t, nodes).ID, "mod:outbound/index"; got != want {
		t.Errorf("module ID = %q, want %q", got, want)
	}
	if !dependencyTargets(edges)["import:./x"] {
		t.Errorf("expected unresolved import:./x without a root, got %v", dependencyTargets(edges))
	}
}

func TestAnalyzeRooted_ResolvesRelativeImports(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/app/service.ts": `import { Entity } from '../domain/entity';
import { helper } from './helper.js';
import { ports } from './ports';
import { View } from './view';
import Widget from './Widget.svelte';
import express from 'express';
import { missing } from './missing';
import { outside } from '../../../outside';
`,
		"src/domain/entity.ts":   "export class Entity {}\n",
		"src/app/helper.ts":      "export const helper = 1;\n",
		"src/app/ports/index.ts": "export const ports = 1;\n",
		"src/app/view.tsx":       "export const View = () => null;\n",
		"src/app/Widget.svelte":  "<script lang=\"ts\"></script>\n",
	})
	// A sibling of the scan root: reachable on disk, not part of the scan.
	if err := os.WriteFile(filepath.Join(filepath.Dir(root), "outside.ts"), []byte("export const outside = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(filepath.Dir(root), "outside.ts")) })

	_, edges, err := New().AnalyzeRooted(root, filepath.Join(root, "src/app/service.ts"))
	if err != nil {
		t.Fatal(err)
	}
	got := dependencyTargets(edges)

	for _, want := range []string{
		"mod:src/domain/entity",     // parent-relative
		"mod:src/app/helper",        // .js specifier maps to the .ts source
		"mod:src/app/ports/index",   // directory import resolves to its index
		"mod:src/app/view",          // .tsx
		"mod:src/app/Widget.svelte", // Svelte component keeps its extension
		"import:express",            // package import
		"import:./missing",          // no such file
		"import:../../../outside",   // resolves outside the scan root
	} {
		if !got[want] {
			t.Errorf("missing dependency target %q; got %v", want, got)
		}
	}
}
