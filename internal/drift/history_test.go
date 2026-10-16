package drift

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initHistoryRepo creates a git repo with multiple commits.
func initHistoryRepo(t *testing.T, count int) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
			// Disable auto-gc and auto-maintenance: commit can spawn them as
			// detached processes that keep writing into .git after the test
			// returns, failing t.TempDir cleanup with "directory not empty".
			"GIT_CONFIG_COUNT=2",
			"GIT_CONFIG_KEY_0=gc.auto",
			"GIT_CONFIG_VALUE_0=0",
			"GIT_CONFIG_KEY_1=maintenance.auto",
			"GIT_CONFIG_VALUE_1=false",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s: %v", args, string(out), err)
		}
	}

	run("init")
	run("checkout", "-b", "main")

	for i := 0; i < count; i++ {
		fname := filepath.Join(dir, "file"+string(rune('a'+i))+".go")
		if err := os.WriteFile(fname, []byte("package main\n"), 0644); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-m", "commit "+string(rune('A'+i)))
	}

	return dir
}

func TestGetSignificantCommits(t *testing.T) {
	repoDir := initHistoryRepo(t, 5)

	commits, err := GetSignificantCommits(context.Background(), repoDir, 10)
	if err != nil {
		t.Fatalf("GetSignificantCommits failed: %v", err)
	}

	if len(commits) != 5 {
		t.Fatalf("expected 5 commits, got %d", len(commits))
	}

	// Most recent first
	if commits[0].Message != "commit E" {
		t.Fatalf("expected most recent commit first, got %s", commits[0].Message)
	}
	if commits[4].Message != "commit A" {
		t.Fatalf("expected oldest commit last, got %s", commits[4].Message)
	}

	// Each entry should have non-empty fields
	for _, c := range commits {
		if c.Hash == "" {
			t.Fatal("commit hash should not be empty")
		}
		if c.Date == "" {
			t.Fatal("commit date should not be empty")
		}
	}
}

func TestGetSignificantCommits_Limit(t *testing.T) {
	repoDir := initHistoryRepo(t, 5)

	commits, err := GetSignificantCommits(context.Background(), repoDir, 2)
	if err != nil {
		t.Fatalf("GetSignificantCommits failed: %v", err)
	}

	if len(commits) != 2 {
		t.Fatalf("expected 2 commits with limit=2, got %d", len(commits))
	}
}

func TestGetSignificantCommits_MaxLimit(t *testing.T) {
	repoDir := initHistoryRepo(t, 3)

	// Limit is capped at 20
	commits, err := GetSignificantCommits(context.Background(), repoDir, 100)
	if err != nil {
		t.Fatalf("GetSignificantCommits failed: %v", err)
	}

	if len(commits) != 3 {
		t.Fatalf("expected 3 commits (repo only has 3), got %d", len(commits))
	}
}
