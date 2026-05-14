package common

import (
	"context"
	"strings"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"

	"github.com/olgasafonova/ridge/internal/model"
)

var testPatterns = []InfraPattern{
	{
		Packages: []string{"pg", "prisma", "typeorm"},
		NodeType: model.NodeDatabase,
		EdgeType: model.EdgeReadWrite,
		NodeID:   "infra:database",
		NodeName: "Database",
	},
	{
		Packages: []string{"redis", "ioredis"},
		NodeType: model.NodeCache,
		EdgeType: model.EdgeReadWrite,
		NodeID:   "infra:cache",
		NodeName: "Cache",
	},
}

func TestClassifyImport_Match(t *testing.T) {
	nodes, edges := ClassifyImport("pg", "mod:test", testPatterns, "/")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Type != model.NodeDatabase {
		t.Fatalf("expected database node, got %s", nodes[0].Type)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
}

func TestClassifyImport_NoMatch(t *testing.T) {
	nodes, edges := ClassifyImport("express", "mod:test", testPatterns, "/")
	if nodes != nil || edges != nil {
		t.Fatal("expected nil for unmatched import")
	}
}

func TestMatchesAny_TSStyle(t *testing.T) {
	patterns := []string{"pg", "prisma"}
	if !MatchesAny("pg", patterns, "/") {
		t.Fatal("expected exact match")
	}
	if !MatchesAny("pg/pool", patterns, "/") {
		t.Fatal("expected prefix match with /")
	}
	if MatchesAny("pgx", patterns, "/") {
		t.Fatal("pgx should not match pg")
	}
}

func TestMatchesAny_PythonStyle(t *testing.T) {
	patterns := []string{"sqlalchemy", "django.db"}
	if !MatchesAny("sqlalchemy", patterns, ".") {
		t.Fatal("expected exact match")
	}
	if !MatchesAny("sqlalchemy.orm", patterns, ".") {
		t.Fatal("expected prefix match with .")
	}
	if MatchesAny("sqlalchemyx", patterns, ".") {
		t.Fatal("sqlalchemyx should not match sqlalchemy")
	}
}

func parseToTree(t *testing.T, src string) (*sitter.Tree, *sitter.Node) {
	t.Helper()
	p := sitter.NewParser()
	p.SetLanguage(python.GetLanguage())
	tree, err := p.ParseCtx(context.Background(), nil, []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return tree, tree.RootNode()
}

// TestWalkTree_NilSafe verifies the walker no-ops on nil instead of panicking.
func TestWalkTree_NilSafe(t *testing.T) {
	called := false
	WalkTree(nil, func(*sitter.Node) { called = true })
	if called {
		t.Fatal("fn must not be called for nil node")
	}
}

// TestWalkTree_PreOrderDFS verifies the iterative walker preserves pre-order
// semantics: parents come before children, leftmost child before rightmost.
// Tree-sitter pre-order DFS yields non-decreasing start_byte values.
func TestWalkTree_PreOrderDFS(t *testing.T) {
	tree, root := parseToTree(t, "x = 1\ny = 2\n")
	defer tree.Close()

	var starts []uint32
	WalkTree(root, func(n *sitter.Node) {
		starts = append(starts, n.StartByte())
	})

	if len(starts) < 3 {
		t.Fatalf("expected several nodes for a 2-line python source, got %d", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if starts[i] < starts[i-1] {
			t.Fatalf("pre-order broken at index %d: start=%d < prev=%d", i, starts[i], starts[i-1])
		}
	}
}

// TestWalkTree_DeepNestingNoOverflow walks a tree built from 20k nested
// parens. The previous recursive implementation grew the goroutine stack
// linearly with depth and triggered runtime.throw on pathological inputs;
// the iterative implementation walks the same tree in heap memory.
func TestWalkTree_DeepNestingNoOverflow(t *testing.T) {
	const depth = 20000
	src := "x = " + strings.Repeat("(", depth) + "1" + strings.Repeat(")", depth) + "\n"

	tree, root := parseToTree(t, src)
	defer tree.Close()

	count := 0
	WalkTree(root, func(*sitter.Node) { count++ })

	if count < depth {
		t.Fatalf("expected walker to visit at least %d nodes, got %d", depth, count)
	}
}
