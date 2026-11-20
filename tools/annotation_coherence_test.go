package tools

import (
	"context"
	"testing"
)

// TestAnnotationCoherence guards against a read-only tool also declaring
// idempotentHint. idempotentHint carries meaning only for tools that modify
// state: a read-only tool is trivially repeatable, so asserting idempotence
// on it says nothing and misleads a client reasoning about retry safety.
//
// This does not forbid Idempotent on its own. It is the right hint for a
// non-read-only tool whose repeat converges — arch_snapshot rewriting the same
// snapshot file, for instance — so the check is on the conjunction.
//
// Annotations are applied centrally in register(), which reads spec.ReadOnly
// and spec.Idempotent, so AllTools is the single source to check.
func TestAnnotationCoherence(t *testing.T) {
	var incoherent []string
	for _, spec := range AllTools {
		if spec.ReadOnly && spec.Idempotent {
			incoherent = append(incoherent, spec.Name)
		}
	}

	if len(incoherent) > 0 {
		t.Errorf("%d of %d specs set both ReadOnly and Idempotent; drop Idempotent on read-only specs: %v",
			len(incoherent), len(AllTools), incoherent)
	}
}

// writeToolAnnotations pins destructiveHint and idempotentHint for every
// non-read-only tool. Destructive is a plain bool on ToolSpec, so a new
// writing tool that forgets to set it would silently emit
// destructiveHint=false; TestEmittedAnnotations fails on any non-read-only
// tool missing from this table, which forces the choice to be made here.
var writeToolAnnotations = map[string]struct{ destructive, idempotent bool }{
	"arch_registry_add":    {destructive: false, idempotent: false}, // additive, refuses an existing alias
	"arch_registry_remove": {destructive: true, idempotent: true},
	"arch_snapshot":        {destructive: true, idempotent: true}, // overwrites the snapshot file
	"arch_validate":        {destructive: true, idempotent: true}, // baseline=write overwrites the baseline file
}

// TestEmittedAnnotations reads annotations off a live ListTools, so it checks
// what a client receives rather than the ToolSpec fields. Every tool is
// local-only, so openWorldHint must be sent as an explicit false (omitted
// means true). Read-only tools must not send destructiveHint; write tools must
// send the value pinned in writeToolAnnotations.
func TestEmittedAnnotations(t *testing.T) {
	session := connectForSchemas(t)

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != len(AllTools) {
		t.Fatalf("ListTools returned %d tools, want %d", len(res.Tools), len(AllTools))
	}

	seenWrite := map[string]bool{}
	for _, tool := range res.Tools {
		a := tool.Annotations
		if a == nil {
			t.Errorf("%s: no annotations emitted", tool.Name)
			continue
		}
		if a.Title == "" {
			t.Errorf("%s: title not emitted", tool.Name)
		}
		if a.OpenWorldHint == nil {
			t.Errorf("%s: openWorldHint omitted (spec default is true); want explicit false", tool.Name)
		} else if *a.OpenWorldHint {
			t.Errorf("%s: openWorldHint = true, want false (local files and git only)", tool.Name)
		}

		if a.ReadOnlyHint {
			if a.DestructiveHint != nil {
				t.Errorf("%s: read-only tool emits destructiveHint=%v; omit it", tool.Name, *a.DestructiveHint)
			}
			continue
		}

		want, ok := writeToolAnnotations[tool.Name]
		if !ok {
			t.Errorf("%s: non-read-only tool missing from writeToolAnnotations; pin its destructive and idempotent values", tool.Name)
			continue
		}
		seenWrite[tool.Name] = true
		if a.DestructiveHint == nil {
			t.Errorf("%s: destructiveHint omitted (spec default is true); want explicit %v", tool.Name, want.destructive)
		} else if *a.DestructiveHint != want.destructive {
			t.Errorf("%s: destructiveHint = %v, want %v", tool.Name, *a.DestructiveHint, want.destructive)
		}
		if a.IdempotentHint != want.idempotent {
			t.Errorf("%s: idempotentHint = %v, want %v", tool.Name, a.IdempotentHint, want.idempotent)
		}
	}

	for name := range writeToolAnnotations {
		if !seenWrite[name] {
			t.Errorf("%s: listed in writeToolAnnotations but not registered as a non-read-only tool", name)
		}
	}
}
