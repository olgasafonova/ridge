package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestArchHistory_RejectsBadInput(t *testing.T) {
	notARepo := t.TempDir()
	writeTestFile(t, notARepo, "go.mod", "module example.com/app\n\ngo 1.22\n")

	tests := []struct {
		name    string
		args    ArchHistoryArgs
		wantErr string
	}{
		{"neither path nor repo", ArchHistoryArgs{}, "either path or repo is required"},
		{"path does not exist", ArchHistoryArgs{Path: "/nonexistent/ridge/fixture"}, "path does not exist"},
		{"not a git repository", ArchHistoryArgs{Path: notARepo}, "getting git history"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewHandlerRegistry(testLogger()).archHistory(context.Background(), tt.args)
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q; want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// The fixture's second commit adds internal/store. With no scan controls the
// newest entry is larger than the older one; with skip_dirs=internal both
// commits scan to the same graph.
func TestArchHistory_AppliesSkipDirsToEveryCommit(t *testing.T) {
	repo, _ := initDriftTestRepo(t)
	h := NewHandlerRegistry(testLogger())

	plain, err := h.archHistory(context.Background(), ArchHistoryArgs{Path: repo})
	if err != nil {
		t.Fatalf("archHistory: %v", err)
	}
	if len(plain.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(plain.Entries))
	}
	newest, oldest := plain.Entries[0], plain.Entries[1]
	if newest.NodeCount <= oldest.NodeCount || newest.ChangesFromPrevious == 0 {
		t.Fatalf("second commit adds a package: newest %+v, oldest %+v", newest, oldest)
	}

	skipped, err := h.archHistory(context.Background(), ArchHistoryArgs{
		Path:        repo,
		ScanControl: ScanControl{SkipDirs: []string{"internal"}},
	})
	if err != nil {
		t.Fatalf("archHistory with skip_dirs: %v", err)
	}
	newest, oldest = skipped.Entries[0], skipped.Entries[1]
	if newest.NodeCount != oldest.NodeCount || newest.ChangesFromPrevious != 0 {
		t.Fatalf("internal/ skipped, so both commits should match: newest %+v, oldest %+v", newest, oldest)
	}
	if newest.NodeCount == 0 {
		t.Fatal("cmd/api should still be scanned")
	}
}

func TestArchHistory_LimitBoundsEntries(t *testing.T) {
	repo, _ := initDriftTestRepo(t)

	res, err := NewHandlerRegistry(testLogger()).archHistory(context.Background(), ArchHistoryArgs{Path: repo, Limit: 1})
	if err != nil {
		t.Fatalf("archHistory: %v", err)
	}
	if len(res.Entries) != 1 || res.Summary != "Analyzed 1 commits" {
		t.Fatalf("got %d entries, summary %q", len(res.Entries), res.Summary)
	}
}

// A client call is checked against the input schema before the handler runs,
// so this is the path that used to answer "unexpected additional properties".
func TestArchHistory_AcceptsScanControlsOverTheWire(t *testing.T) {
	repo, _ := initDriftTestRepo(t)
	session := connectForSchemas(t)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "arch_history",
		Arguments: map[string]any{
			"path":          repo,
			"skip_dirs":     []string{"internal"},
			"skip_globs":    []string{"**/*.md"},
			"include_tests": true,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("arch_history rejected the call: %+v", res.Content)
	}
}
