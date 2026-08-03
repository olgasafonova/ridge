package tools

import "testing"

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
