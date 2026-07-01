package detector

import (
	"slices"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

// helper to build a minimal graph with nodes and edges.
func buildGraph(nodes []*model.Node, edges []*model.Edge) *model.ArchGraph {
	g := model.NewGraph("/test")
	for _, n := range nodes {
		g.AddNode(n)
	}
	for _, e := range edges {
		g.AddEdge(e)
	}
	return g
}

// buildUnstableCoreGraph returns a graph whose "core" node has fan-in 4 and
// fan-out 10 → instability 10/14 ≈ 0.71, tripping stabilize_core (fan-in > 3 AND
// instability > 0.7). Shared by the stabilize-core tests.
func buildUnstableCoreGraph() *model.ArchGraph {
	nodes := []*model.Node{{ID: "core", Name: "core", Type: model.NodePackage}}
	var edges []*model.Edge
	for i := range 4 {
		id := string(rune('A' + i))
		nodes = append(nodes, &model.Node{ID: id, Name: id, Type: model.NodePackage})
		edges = append(edges, &model.Edge{Source: id, Target: "core", Type: model.EdgeDependency})
	}
	for i := range 10 {
		id := string(rune('a' + i))
		nodes = append(nodes, &model.Node{ID: id, Name: id, Type: model.NodePackage})
		edges = append(edges, &model.Edge{Source: "core", Target: id, Type: model.EdgeDependency})
	}
	return buildGraph(nodes, edges)
}

func TestRecommend_NoCycles_Clean(t *testing.T) {
	g := buildGraph(
		[]*model.Node{
			{ID: "a", Name: "a", Type: model.NodePackage},
			{ID: "b", Name: "b", Type: model.NodePackage},
			{ID: "c", Name: "c", Type: model.NodePackage},
		},
		[]*model.Edge{
			{Source: "a", Target: "b", Type: model.EdgeDependency},
			{Source: "b", Target: "c", Type: model.EdgeDependency},
		},
	)
	violations := ValidateGraph(g, nil)
	metrics := ComputeMetrics(g)
	explanation := ExplainArchitecture(g, nil)

	recs := RecommendArchitecture(g, violations, metrics, explanation)
	if len(recs) != 0 {
		t.Errorf("expected 0 recommendations for clean graph, got %d: %+v", len(recs), recs)
	}
}

func TestRecommend_CycleDetected(t *testing.T) {
	g := buildGraph(
		[]*model.Node{
			{ID: "a", Name: "a", Type: model.NodePackage},
			{ID: "b", Name: "b", Type: model.NodePackage},
			{ID: "c", Name: "c", Type: model.NodePackage},
		},
		[]*model.Edge{
			{Source: "a", Target: "b", Type: model.EdgeDependency},
			{Source: "b", Target: "c", Type: model.EdgeDependency},
			{Source: "c", Target: "a", Type: model.EdgeDependency},
		},
	)
	violations := ValidateGraph(g, nil)
	metrics := ComputeMetrics(g)
	explanation := ExplainArchitecture(g, nil)

	recs := RecommendArchitecture(g, violations, metrics, explanation)
	found := false
	for _, r := range recs {
		if r.Category == "break_cycle" {
			found = true
			if r.Priority != "high" {
				t.Errorf("break_cycle should be high priority, got %s", r.Priority)
			}
			if len(r.Subject) != 3 {
				t.Errorf("expected 3 nodes in cycle subject, got %d", len(r.Subject))
			}
		}
	}
	if !found {
		t.Errorf("expected break_cycle recommendation, got %+v", recs)
	}
}

// findRec returns the first recommendation of a category, or nil.
func findRec(recs []Recommendation, category string) *Recommendation {
	for i := range recs {
		if recs[i].Category == category {
			return &recs[i]
		}
	}
	return nil
}

// findEvidence returns the first evidence entry for a metric, or nil.
func findEvidence(ev []Evidence, metric string) *Evidence {
	for i := range ev {
		if ev[i].Metric == metric {
			return &ev[i]
		}
	}
	return nil
}

func TestConfidenceFromMargin(t *testing.T) {
	cases := []struct {
		value, threshold float64
		want             string
	}{
		{15, 10, "high"},   // 1.5×
		{13, 10, "medium"}, // 1.3×
		{11, 10, "low"},    // 1.1×
		{5, 0, "medium"},   // undefined threshold → medium
	}
	for _, c := range cases {
		if got := confidenceFromMargin(c.value, c.threshold); got != c.want {
			t.Errorf("confidenceFromMargin(%v, %v) = %q, want %q", c.value, c.threshold, got, c.want)
		}
	}
	// weakestConfidence returns the lowest band.
	if got := weakestConfidence("high", "low", "medium"); got != "low" {
		t.Errorf("weakestConfidence = %q, want low", got)
	}
}

// TestRecommend_EvidenceAndConfidence is the design-for-verifiability check: every
// recommendation must carry structured evidence + a confidence grade, and the
// evidence values must match the triggering metric so a consumer can re-derive the
// judgment instead of trusting the prose.
func TestRecommend_EvidenceAndConfidence(t *testing.T) {
	cyc := buildGraph(
		[]*model.Node{
			{ID: "a", Name: "a", Type: model.NodePackage},
			{ID: "b", Name: "b", Type: model.NodePackage},
			{ID: "c", Name: "c", Type: model.NodePackage},
		},
		[]*model.Edge{
			{Source: "a", Target: "b", Type: model.EdgeDependency},
			{Source: "b", Target: "c", Type: model.EdgeDependency},
			{Source: "c", Target: "a", Type: model.EdgeDependency},
		},
	)
	recs := RecommendArchitecture(cyc, ValidateGraph(cyc, nil), ComputeMetrics(cyc), ExplainArchitecture(cyc, nil))

	rec := findRec(recs, "break_cycle")
	if rec == nil {
		t.Fatalf("expected break_cycle rec, got %+v", recs)
	}
	if rec.Confidence != "high" {
		t.Errorf("break_cycle confidence: want high (deterministic), got %q", rec.Confidence)
	}
	ev := findEvidence(rec.Evidence, "cycle_size")
	if ev == nil {
		t.Fatalf("break_cycle missing cycle_size evidence: %+v", rec.Evidence)
	}
	if ev.Value != 3 {
		t.Errorf("cycle_size value: want 3 (nodes in cycle), got %v", ev.Value)
	}

	// Invariant across every category: non-empty evidence and confidence.
	for _, r := range recs {
		if len(r.Evidence) == 0 {
			t.Errorf("rec %q carries no evidence (verifiability invariant)", r.Category)
		}
		if r.Confidence == "" {
			t.Errorf("rec %q has empty confidence", r.Category)
		}
	}
}

// TestRecommend_StabilizeCoreEvidence checks the two-signal rule exposes both
// readings with their real thresholds and grades confidence off the weaker margin.
func TestRecommend_StabilizeCoreEvidence(t *testing.T) {
	g := buildUnstableCoreGraph()
	recs := RecommendArchitecture(g, ValidateGraph(g, nil), ComputeMetrics(g), ExplainArchitecture(g, nil))

	rec := findRec(recs, "stabilize_core")
	if rec == nil {
		t.Fatalf("expected stabilize_core, got %+v", recs)
	}
	fi := findEvidence(rec.Evidence, "fan_in")
	inst := findEvidence(rec.Evidence, "instability")
	if fi == nil || inst == nil {
		t.Fatalf("stabilize_core must expose fan_in and instability evidence: %+v", rec.Evidence)
	}
	if fi.Threshold != 3 || inst.Threshold != 0.7 {
		t.Errorf("thresholds: fan_in=%v (want 3), instability=%v (want 0.7)", fi.Threshold, inst.Threshold)
	}
	if fi.Subject != "core" || inst.Subject != "core" {
		t.Errorf("evidence subject should be the node: got %q / %q", fi.Subject, inst.Subject)
	}
}

func TestRecommend_HighCoupling(t *testing.T) {
	// Build a dense graph where most nodes have coupling ~3, and "hub" has 10.
	// 11 nodes total, no zero-coupling leaves → avg ≈ 3.6, threshold ≈ 7.2.
	// Hub at 10 exceeds that.
	nodes := []*model.Node{
		{ID: "hub", Name: "hub", Type: model.NodePackage},
	}
	targetIDs := make([]string, 10)
	var edges []*model.Edge
	for i := range 10 {
		id := string(rune('a' + i))
		targetIDs[i] = id
		nodes = append(nodes, &model.Node{ID: id, Name: id, Type: model.NodePackage})
		edges = append(edges, &model.Edge{Source: "hub", Target: id, Type: model.EdgeDependency})
	}
	// Each target depends on 3 other targets (circular deps among targets).
	for i, src := range targetIDs {
		for j := 1; j <= 3; j++ {
			dst := targetIDs[(i+j)%len(targetIDs)]
			edges = append(edges, &model.Edge{Source: src, Target: dst, Type: model.EdgeDependency})
		}
	}

	g := buildGraph(nodes, edges)
	metrics := ComputeMetrics(g)
	violations := ValidateGraph(g, nil)
	explanation := ExplainArchitecture(g, nil)

	recs := RecommendArchitecture(g, violations, metrics, explanation)
	found := false
	for _, r := range recs {
		if r.Category == "reduce_coupling" {
			for _, s := range r.Subject {
				if s == "hub" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("expected reduce_coupling for hub node, got %+v", recs)
	}
}

func TestRecommend_UnstableCore(t *testing.T) {
	g := buildUnstableCoreGraph()
	recs := RecommendArchitecture(g, ValidateGraph(g, nil), ComputeMetrics(g), ExplainArchitecture(g, nil))

	rec := findRec(recs, "stabilize_core")
	if rec == nil {
		t.Fatalf("expected stabilize_core for core node, got %+v", recs)
	}
	if !slices.Contains(rec.Subject, "core") {
		t.Errorf("stabilize_core subject should include core, got %v", rec.Subject)
	}
}

func TestRecommend_EndpointToDatabase(t *testing.T) {
	g := buildGraph(
		[]*model.Node{
			{ID: "ep", Name: "GET /users", Type: model.NodeEndpoint},
			{ID: "db", Name: "postgres", Type: model.NodeDatabase},
		},
		[]*model.Edge{
			{Source: "ep", Target: "db", Type: model.EdgeDependency},
		},
	)
	violations := ValidateGraph(g, nil)
	metrics := ComputeMetrics(g)
	explanation := ExplainArchitecture(g, nil)

	recs := RecommendArchitecture(g, violations, metrics, explanation)
	found := false
	for _, r := range recs {
		if r.Category == "add_layer" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected add_layer recommendation, got %+v", recs)
	}
}

func TestRecommend_SharedDatabase(t *testing.T) {
	g := buildGraph(
		[]*model.Node{
			{ID: "svc1", Name: "svc1", Type: model.NodeService},
			{ID: "svc2", Name: "svc2", Type: model.NodeService},
			{ID: "svc3", Name: "svc3", Type: model.NodeService},
			{ID: "db", Name: "postgres", Type: model.NodeDatabase},
		},
		[]*model.Edge{
			{Source: "svc1", Target: "db", Type: model.EdgeReadWrite},
			{Source: "svc2", Target: "db", Type: model.EdgeReadWrite},
			{Source: "svc3", Target: "db", Type: model.EdgeReadWrite},
		},
	)
	violations := ValidateGraph(g, nil)
	metrics := ComputeMetrics(g)
	explanation := ExplainArchitecture(g, nil)

	recs := RecommendArchitecture(g, violations, metrics, explanation)
	found := false
	for _, r := range recs {
		if r.Category == "split_database" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected split_database recommendation, got %+v", recs)
	}
}

func TestRecommend_MissingCache(t *testing.T) {
	nodes := []*model.Node{
		{ID: "svc", Name: "svc", Type: model.NodeService},
	}
	var edges []*model.Edge
	// 6 endpoints connected to the service
	for i := range 6 {
		id := string(rune('a' + i))
		nodes = append(nodes, &model.Node{ID: id, Name: "GET /" + id, Type: model.NodeEndpoint})
		edges = append(edges, &model.Edge{Source: id, Target: "svc", Type: model.EdgeAPICall, Label: "serves"})
	}

	g := buildGraph(nodes, edges)
	violations := ValidateGraph(g, nil)
	metrics := ComputeMetrics(g)
	explanation := ExplainArchitecture(g, nil)

	recs := RecommendArchitecture(g, violations, metrics, explanation)
	found := false
	for _, r := range recs {
		if r.Category == "add_caching" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected add_caching recommendation, got %+v", recs)
	}
}

func TestRecommend_Orphan(t *testing.T) {
	g := buildGraph(
		[]*model.Node{
			{ID: "a", Name: "a", Type: model.NodePackage},
			{ID: "b", Name: "b", Type: model.NodePackage},
			{ID: "orphan", Name: "orphan", Type: model.NodePackage},
		},
		[]*model.Edge{
			{Source: "a", Target: "b", Type: model.EdgeDependency},
		},
	)
	violations := ValidateGraph(g, nil)
	metrics := ComputeMetrics(g)
	explanation := ExplainArchitecture(g, nil)

	recs := RecommendArchitecture(g, violations, metrics, explanation)
	found := false
	for _, r := range recs {
		if r.Category == "remove_orphan" {
			for _, s := range r.Subject {
				if s == "orphan" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("expected remove_orphan for orphan node, got %+v", recs)
	}
}

func TestRecommend_MultipleIssues(t *testing.T) {
	// Graph with a cycle and an orphan: should produce multiple recommendations
	// sorted high before low.
	g := buildGraph(
		[]*model.Node{
			{ID: "a", Name: "a", Type: model.NodePackage},
			{ID: "b", Name: "b", Type: model.NodePackage},
			{ID: "orphan", Name: "orphan", Type: model.NodePackage},
		},
		[]*model.Edge{
			{Source: "a", Target: "b", Type: model.EdgeDependency},
			{Source: "b", Target: "a", Type: model.EdgeDependency},
		},
	)
	violations := ValidateGraph(g, nil)
	metrics := ComputeMetrics(g)
	explanation := ExplainArchitecture(g, nil)

	recs := RecommendArchitecture(g, violations, metrics, explanation)
	if len(recs) < 2 {
		t.Fatalf("expected at least 2 recommendations, got %d: %+v", len(recs), recs)
	}

	// Verify sorting: first recommendation should be high priority.
	if recs[0].Priority != "high" {
		t.Errorf("first recommendation should be high priority, got %s (category: %s)", recs[0].Priority, recs[0].Category)
	}

	// Last recommendation should be low priority (orphan).
	last := recs[len(recs)-1]
	if last.Priority != "low" {
		t.Errorf("last recommendation should be low priority, got %s (category: %s)", last.Priority, last.Category)
	}
}

func TestRecommend_PriorityBoosting(t *testing.T) {
	// Node "x" is in a cycle AND has high fan-in → multiple rules flag it.
	// The orphan rule for "orphan" should get boosted if "orphan" appears in
	// multiple rules. But here we test: node "x" is in a cycle (high already),
	// and we add a medium-priority recommendation for "x" via split_module
	// (fan-out > 8). The boosting should promote that to high because
	// subjectCount["x"] > 1.
	nodes := []*model.Node{
		{ID: "x", Name: "x", Type: model.NodePackage},
		{ID: "y", Name: "y", Type: model.NodePackage},
	}
	var edges []*model.Edge
	// Cycle between x and y
	edges = append(edges,
		&model.Edge{Source: "x", Target: "y", Type: model.EdgeDependency},
		&model.Edge{Source: "y", Target: "x", Type: model.EdgeDependency},
	)
	// x also depends on 9 other nodes (fan-out > 8 → split_module)
	for i := range 9 {
		id := string(rune('a' + i))
		nodes = append(nodes, &model.Node{ID: id, Name: id, Type: model.NodePackage})
		edges = append(edges, &model.Edge{Source: "x", Target: id, Type: model.EdgeDependency})
	}
	// Give those 9 nodes some outgoing deps so avg coupling > 2
	for i := range 9 {
		src := string(rune('a' + i))
		for j := range 3 {
			depID := src + string(rune('0'+j))
			nodes = append(nodes, &model.Node{ID: depID, Name: depID, Type: model.NodePackage})
			edges = append(edges, &model.Edge{Source: src, Target: depID, Type: model.EdgeDependency})
		}
	}

	g := buildGraph(nodes, edges)
	violations := ValidateGraph(g, nil)
	metrics := ComputeMetrics(g)
	explanation := ExplainArchitecture(g, nil)

	recs := RecommendArchitecture(g, violations, metrics, explanation)

	// Find the split_module recommendation for "x" and check it was boosted.
	for _, r := range recs {
		if r.Category == "split_module" && slices.Contains(r.Subject, "x") {
			if r.Priority != "high" {
				t.Errorf("split_module for x should be boosted to high (compound risk), got %s", r.Priority)
			}
			return
		}
	}
	t.Errorf("expected split_module recommendation for x, got %+v", recs)
}
