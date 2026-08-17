package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRegistryTestHandler builds a HandlerRegistry whose repo registry is rooted
// in a throwaway HOME. registry.Load resolves ~/.mcp-context/ridge via
// os.UserHomeDir, so without this every add/remove test would write into the
// developer's real state directory.
func newRegistryTestHandler(t *testing.T) *HandlerRegistry {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return NewHandlerRegistry(testLogger())
}

// TestArchRegistryHandlers_NoRegistry covers the degraded path where
// registry.Load failed at startup and repoRegistry is nil: the two mutating
// handlers refuse, and list reports an empty set rather than erroring.
func TestArchRegistryHandlers_NoRegistry(t *testing.T) {
	h := &HandlerRegistry{logger: testLogger()}
	ctx := context.Background()

	if _, err := h.archRegistryAdd(ctx, ArchRegistryAddArgs{Path: t.TempDir()}); err == nil {
		t.Error("archRegistryAdd: expected an error when the registry is unavailable")
	}
	if _, err := h.archRegistryRemove(ctx, ArchRegistryRemoveArgs{Alias: "anything"}); err == nil {
		t.Error("archRegistryRemove: expected an error when the registry is unavailable")
	}

	res, err := h.archRegistryList(ctx, ArchRegistryListArgs{})
	if err != nil {
		t.Fatalf("archRegistryList should degrade rather than fail: %v", err)
	}
	if res.Repos == nil {
		t.Error("Repos should be an empty slice, not nil, so the JSON result is [] rather than null")
	}
	if len(res.Repos) != 0 {
		t.Errorf("Repos: want 0 entries, got %d", len(res.Repos))
	}
	if !strings.Contains(res.Summary, "not available") {
		t.Errorf("Summary should say why the list is empty, got %q", res.Summary)
	}
}

// TestArchRegistryAdd_RejectsBadInput covers the validation gates in front of
// registry.Add: the scan-path checks and the alias slug check that keeps
// StatePath from being steered out of the state directory.
func TestArchRegistryAdd_RejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		args    ArchRegistryAddArgs
		wantErr string
	}{
		{"empty path", ArchRegistryAddArgs{}, "path is required"},
		{"path does not exist", ArchRegistryAddArgs{Path: filepath.Join(dir, "missing")}, "path does not exist"},
		{"path is a file", ArchRegistryAddArgs{Path: file}, "path is not a directory"},
		{"alias traverses out of state dir", ArchRegistryAddArgs{Path: dir, Alias: "../../tmp/victim"}, "invalid alias"},
		{"alias contains a slash", ArchRegistryAddArgs{Path: dir, Alias: "team/repo"}, "invalid alias"},
		{"alias starts with a dot", ArchRegistryAddArgs{Path: dir, Alias: ".hidden"}, "invalid alias"},
		{"alias too long", ArchRegistryAddArgs{Path: dir, Alias: strings.Repeat("a", 65)}, "invalid alias"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newRegistryTestHandler(t)
			_, err := h.archRegistryAdd(context.Background(), tt.args)
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q; want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestArchRegistryRemove_RejectsBadInput covers the two refusals remove owns:
// a missing alias argument and an alias that was never registered.
func TestArchRegistryRemove_RejectsBadInput(t *testing.T) {
	tests := []struct {
		name    string
		alias   string
		wantErr string
	}{
		{"empty alias", "", "alias is required"},
		{"unknown alias", "never-registered", "not found in registry"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newRegistryTestHandler(t)
			_, err := h.archRegistryRemove(context.Background(), ArchRegistryRemoveArgs{Alias: tt.alias})
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q; want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestArchRegistry_RoundTrip walks add -> list -> duplicate-add -> remove ->
// list against a real on-disk registry under a temp HOME, which is the only way
// to see that each handler's Save actually persisted.
func TestArchRegistry_RoundTrip(t *testing.T) {
	h := newRegistryTestHandler(t)
	ctx := context.Background()
	repo := t.TempDir()

	added, err := h.archRegistryAdd(ctx, ArchRegistryAddArgs{Path: repo, Alias: "myrepo"})
	if err != nil {
		t.Fatalf("archRegistryAdd failed: %v", err)
	}
	if added.Alias != "myrepo" {
		t.Errorf("Alias: want myrepo, got %q", added.Alias)
	}
	if !filepath.IsAbs(added.Path) {
		t.Errorf("Path should be absolute, got %q", added.Path)
	}
	if !strings.Contains(added.Summary, "myrepo") {
		t.Errorf("Summary should name the alias, got %q", added.Summary)
	}

	listed, err := h.archRegistryList(ctx, ArchRegistryListArgs{})
	if err != nil {
		t.Fatalf("archRegistryList failed: %v", err)
	}
	if len(listed.Repos) != 1 {
		t.Fatalf("Repos: want 1 entry after add, got %d", len(listed.Repos))
	}
	if listed.Repos[0].Alias != "myrepo" {
		t.Errorf("listed alias: want myrepo, got %q", listed.Repos[0].Alias)
	}
	if listed.Repos[0].Stale {
		t.Error("the registered path exists, so Stale should be false")
	}
	if !strings.Contains(listed.Summary, "1 repos") {
		t.Errorf("Summary should carry the count, got %q", listed.Summary)
	}

	if _, err := h.archRegistryAdd(ctx, ArchRegistryAddArgs{Path: repo, Alias: "myrepo"}); err == nil {
		t.Error("expected an error when re-adding an alias that is already taken")
	}

	removed, err := h.archRegistryRemove(ctx, ArchRegistryRemoveArgs{Alias: "myrepo"})
	if err != nil {
		t.Fatalf("archRegistryRemove failed: %v", err)
	}
	if removed.Alias != "myrepo" || !strings.Contains(removed.Summary, "Removed") {
		t.Errorf("unexpected remove result: %+v", removed)
	}

	listed, err = h.archRegistryList(ctx, ArchRegistryListArgs{})
	if err != nil {
		t.Fatalf("archRegistryList after remove failed: %v", err)
	}
	if len(listed.Repos) != 0 {
		t.Errorf("Repos: want 0 entries after remove, got %d", len(listed.Repos))
	}
}

// TestArchRegistryAdd_DefaultsAliasToBasename pins the documented default:
// omitting alias registers the repo under its directory name.
func TestArchRegistryAdd_DefaultsAliasToBasename(t *testing.T) {
	h := newRegistryTestHandler(t)
	repo := filepath.Join(t.TempDir(), "ridge-fixture")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	added, err := h.archRegistryAdd(context.Background(), ArchRegistryAddArgs{Path: repo})
	if err != nil {
		t.Fatalf("archRegistryAdd failed: %v", err)
	}
	if added.Alias != "ridge-fixture" {
		t.Errorf("Alias: want the directory basename ridge-fixture, got %q", added.Alias)
	}
}

// TestArchRegistry_ResolvesRepoAliasForOtherTools checks the reason the
// registry exists: once an alias is registered, resolveRepoPath answers with
// the stored path so every other tool can take repo instead of path.
func TestArchRegistry_ResolvesRepoAliasForOtherTools(t *testing.T) {
	h := newRegistryTestHandler(t)
	repo := t.TempDir()

	if _, err := h.archRegistryAdd(context.Background(), ArchRegistryAddArgs{Path: repo, Alias: "aliased"}); err != nil {
		t.Fatalf("archRegistryAdd failed: %v", err)
	}

	path, alias, err := h.resolveRepoPath("", "aliased")
	if err != nil {
		t.Fatalf("resolveRepoPath failed: %v", err)
	}
	if alias != "aliased" {
		t.Errorf("alias: want aliased, got %q", alias)
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		t.Fatal(err)
	}
	if path != abs {
		t.Errorf("path: want %q, got %q", abs, path)
	}
}
