package tools

import (
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

// TestClipFullDetail covers the HG-2 emit-side cap on arch_scan detail="full":
// node/edge slices are bounded and Truncated is flagged when anything is dropped.
func TestClipFullDetail(t *testing.T) {
	makeRes := func(nodes, edges int) *ArchScanResult {
		res := &ArchScanResult{}
		for i := 0; i < nodes; i++ {
			res.Nodes = append(res.Nodes, &model.Node{ID: "n"})
		}
		for i := 0; i < edges; i++ {
			res.Edges = append(res.Edges, &model.Edge{Source: "a", Target: "b"})
		}
		return res
	}

	t.Run("default cap clips and flags truncated", func(t *testing.T) {
		res := makeRes(maxFullDetailNodes+50, maxFullDetailNodes+10)
		clipFullDetail(res, 0)
		if len(res.Nodes) != maxFullDetailNodes {
			t.Errorf("nodes: want %d, got %d", maxFullDetailNodes, len(res.Nodes))
		}
		if len(res.Edges) != maxFullDetailNodes {
			t.Errorf("edges: want %d, got %d", maxFullDetailNodes, len(res.Edges))
		}
		if !res.Truncated {
			t.Error("expected Truncated=true after clipping")
		}
	})

	t.Run("under cap leaves slices intact and does not flag", func(t *testing.T) {
		res := makeRes(10, 5)
		clipFullDetail(res, 0)
		if len(res.Nodes) != 10 || len(res.Edges) != 5 {
			t.Errorf("under-cap slices should be untouched, got %d nodes / %d edges", len(res.Nodes), len(res.Edges))
		}
		if res.Truncated {
			t.Error("under-cap result should not be flagged Truncated")
		}
	})

	t.Run("explicit limit overrides default", func(t *testing.T) {
		res := makeRes(20, 20)
		clipFullDetail(res, 5)
		if len(res.Nodes) != 5 || len(res.Edges) != 5 {
			t.Errorf("explicit limit 5 not applied, got %d nodes / %d edges", len(res.Nodes), len(res.Edges))
		}
		if !res.Truncated {
			t.Error("expected Truncated=true when explicit limit clips")
		}
	})
}
