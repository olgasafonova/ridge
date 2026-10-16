package drift

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initGitRepo creates a temp git repo with an initial commit.
func initGitRepo(t *testing.T) string {
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

	// First commit
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "main.go")
	run("commit", "-m", "first commit")

	// Second commit
	if err := os.WriteFile(filepath.Join(dir, "utils.go"), []byte("package main\n\nfunc helper() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "utils.go")
	run("commit", "-m", "second commit")

	return dir
}

func TestCheckoutRef_ValidRef(t *testing.T) {
	repoDir := initGitRepo(t)

	// Get the first commit hash
	cmd := exec.Command("git", "rev-list", "--max-parents=0", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-list failed: %v", err)
	}
	firstCommit := string(out[:len(out)-1]) // trim newline

	worktree, cleanup, err := CheckoutRef(context.Background(), repoDir, firstCommit)
	if err != nil {
		t.Fatalf("CheckoutRef failed: %v", err)
	}
	defer cleanup()

	// The first commit only has main.go, not utils.go
	if _, err := os.Stat(filepath.Join(worktree, "main.go")); err != nil {
		t.Fatal("expected main.go in worktree")
	}
	if _, err := os.Stat(filepath.Join(worktree, "utils.go")); err == nil {
		t.Fatal("utils.go should not exist at first commit")
	}
}

func TestCheckoutRef_InvalidRef(t *testing.T) {
	repoDir := initGitRepo(t)

	_, _, err := CheckoutRef(context.Background(), repoDir, "nonexistent-ref-abc123")
	if err == nil {
		t.Fatal("expected error for invalid ref")
	}
}

func TestCheckoutRef_Cleanup(t *testing.T) {
	repoDir := initGitRepo(t)

	worktree, cleanup, err := CheckoutRef(context.Background(), repoDir, "HEAD")
	if err != nil {
		t.Fatalf("CheckoutRef failed: %v", err)
	}

	// Verify worktree exists
	if _, err := os.Stat(worktree); err != nil {
		t.Fatal("worktree should exist before cleanup")
	}

	cleanup()

	// Verify worktree is removed
	if _, err := os.Stat(worktree); err == nil {
		t.Fatal("worktree should be removed after cleanup")
	}
}

func TestCheckoutRef_HEAD(t *testing.T) {
	repoDir := initGitRepo(t)

	worktree, cleanup, err := CheckoutRef(context.Background(), repoDir, "HEAD")
	if err != nil {
		t.Fatalf("CheckoutRef HEAD failed: %v", err)
	}
	defer cleanup()

	// HEAD should have both files
	if _, err := os.Stat(filepath.Join(worktree, "main.go")); err != nil {
		t.Fatal("expected main.go at HEAD")
	}
	if _, err := os.Stat(filepath.Join(worktree, "utils.go")); err != nil {
		t.Fatal("expected utils.go at HEAD")
	}
}

func TestValidateRef_Valid(t *testing.T) {
	for _, ref := range []string{"main", "v1.0", "abc123", "HEAD", "feature/foo", "HEAD~1", "HEAD^2"} {
		if err := ValidateRef(ref); err != nil {
			t.Errorf("expected valid ref %q, got: %v", ref, err)
		}
	}
}

func TestValidateRef_Empty(t *testing.T) {
	if err := ValidateRef(""); err == nil {
		t.Fatal("expected error for empty ref")
	}
}

func TestValidateRef_DashPrefix(t *testing.T) {
	if err := ValidateRef("--flag"); err == nil {
		t.Fatal("expected error for dash-prefixed ref")
	}
}

func TestValidateRef_InvalidChars(t *testing.T) {
	for _, ref := range []string{"foo;bar", "ref$(cmd)", "a b", "foo`bar`", "ref|pipe"} {
		if err := ValidateRef(ref); err == nil {
			t.Errorf("expected error for ref with invalid chars: %q", ref)
		}
	}
}
