package scanner

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

// fakeAnalyzer is a minimal in-test implementation of the Analyzer interface.
// It lets the conformance tests pin the documented behavioral contract without
// pulling in a language-specific analyzer (which would couple the scanner
// package's interface test to tree-sitter / go-ast detail).
type fakeAnalyzer struct {
	exts      []string
	lang      string
	sig       string
	cloneSeen *int // incremented on each Clone, shared so the test can assert call count
	nodes     []*model.Node
	edges     []*model.Edge
	err       error
}

func (f *fakeAnalyzer) Analyze(path string) ([]*model.Node, []*model.Edge, error) {
	return f.nodes, f.edges, f.err
}

func (f *fakeAnalyzer) Extensions() []string { return f.exts }

func (f *fakeAnalyzer) Language() string { return f.lang }

func (f *fakeAnalyzer) Signature() string { return f.sig }

func (f *fakeAnalyzer) Clone() Analyzer {
	if f.cloneSeen != nil {
		*f.cloneSeen++
	}
	// Return an independent copy: a fresh struct with copied slices so mutation
	// of one instance cannot leak into the other.
	exts := append([]string(nil), f.exts...)
	return &fakeAnalyzer{
		exts:      exts,
		lang:      f.lang,
		sig:       f.sig,
		cloneSeen: f.cloneSeen,
		nodes:     f.nodes,
		edges:     f.edges,
		err:       f.err,
	}
}

// compile-time assertion that fakeAnalyzer satisfies the interface.
var _ Analyzer = (*fakeAnalyzer)(nil)

func TestAnalyzer_NewBuildsExtensionMap(t *testing.T) {
	tests := []struct {
		name      string
		analyzers []Analyzer
		wantExts  []string
	}{
		{
			name:      "single analyzer single extension",
			analyzers: []Analyzer{&fakeAnalyzer{exts: []string{".go"}, lang: "Go"}},
			wantExts:  []string{".go"},
		},
		{
			name:      "single analyzer multiple extensions",
			analyzers: []Analyzer{&fakeAnalyzer{exts: []string{".ts", ".tsx"}, lang: "TypeScript"}},
			wantExts:  []string{".ts", ".tsx"},
		},
		{
			name: "two analyzers distinct extensions",
			analyzers: []Analyzer{
				&fakeAnalyzer{exts: []string{".go"}, lang: "Go"},
				&fakeAnalyzer{exts: []string{".py"}, lang: "Python"},
			},
			wantExts: []string{".go", ".py"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(slog.Default(), tt.analyzers...)
			for _, ext := range tt.wantExts {
				if _, ok := s.analyzers[ext]; !ok {
					t.Fatalf("New did not register extension %q; map=%v", ext, s.analyzers)
				}
			}
			if len(s.analyzers) != len(tt.wantExts) {
				t.Fatalf("expected %d registered extensions, got %d", len(tt.wantExts), len(s.analyzers))
			}
		})
	}
}

func TestAnalyzer_NewLastWriterWinsOnExtensionCollision(t *testing.T) {
	first := &fakeAnalyzer{exts: []string{".go"}, lang: "First"}
	second := &fakeAnalyzer{exts: []string{".go"}, lang: "Second"}

	s := New(slog.Default(), first, second)

	if len(s.analyzers) != 1 {
		t.Fatalf("colliding extension should yield one entry, got %d", len(s.analyzers))
	}
	got := s.analyzers[".go"].Language()
	if got != "Second" {
		t.Fatalf("last analyzer registered for an extension should win: got %q, want %q", got, "Second")
	}
}

func TestAnalyzer_CloneReturnsIndependentCopy(t *testing.T) {
	seen := 0
	orig := &fakeAnalyzer{exts: []string{".go"}, lang: "Go", sig: "v1", cloneSeen: &seen}

	clone := orig.Clone()

	if seen != 1 {
		t.Fatalf("Clone should have been invoked exactly once, counter=%d", seen)
	}
	if clone == Analyzer(orig) {
		t.Fatal("Clone must return a distinct instance, not the same pointer")
	}

	// Mutating the clone's extension slice must not affect the original: the
	// contract says clones are independent copies safe for separate goroutines.
	cf, ok := clone.(*fakeAnalyzer)
	if !ok {
		t.Fatalf("clone has unexpected dynamic type %T", clone)
	}
	cf.exts[0] = ".mutated"
	if orig.exts[0] != ".go" {
		t.Fatalf("mutating clone leaked into original: original ext=%q", orig.exts[0])
	}
}

func TestAnalyzer_AnalyzeReturnShape(t *testing.T) {
	wantErr := errors.New("boom")
	tests := []struct {
		name      string
		analyzer  *fakeAnalyzer
		wantNodes int
		wantEdges int
		wantErr   error
	}{
		{
			name:      "empty result",
			analyzer:  &fakeAnalyzer{},
			wantNodes: 0,
			wantEdges: 0,
			wantErr:   nil,
		},
		{
			name: "nodes and edges returned",
			analyzer: &fakeAnalyzer{
				nodes: []*model.Node{{ID: "pkg:a", Type: model.NodeType("package")}},
				edges: []*model.Edge{{Source: "pkg:a", Target: "pkg:b", Type: model.EdgeType("depends_on")}},
			},
			wantNodes: 1,
			wantEdges: 1,
			wantErr:   nil,
		},
		{
			name:      "error propagates",
			analyzer:  &fakeAnalyzer{err: wantErr},
			wantNodes: 0,
			wantEdges: 0,
			wantErr:   wantErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, edges, err := tt.analyzer.Analyze("any/path.go")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Analyze err = %v, want %v", err, tt.wantErr)
			}
			if len(nodes) != tt.wantNodes {
				t.Fatalf("Analyze nodes = %d, want %d", len(nodes), tt.wantNodes)
			}
			if len(edges) != tt.wantEdges {
				t.Fatalf("Analyze edges = %d, want %d", len(edges), tt.wantEdges)
			}
		})
	}
}

func TestAnalyzer_SignatureStableAcrossClone(t *testing.T) {
	orig := &fakeAnalyzer{exts: []string{".go"}, sig: "go@7"}
	clone := orig.Clone()

	if orig.Signature() != clone.Signature() {
		t.Fatalf("Clone must preserve Signature: orig=%q clone=%q", orig.Signature(), clone.Signature())
	}
	if orig.Signature() == "" {
		t.Fatal("Signature must be non-empty per the interface contract")
	}
}
