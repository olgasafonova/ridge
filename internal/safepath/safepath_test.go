package safepath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateScanPath_Valid(t *testing.T) {
	dir := t.TempDir()
	if err := ValidateScanPath(dir); err != nil {
		t.Fatalf("expected valid path, got: %v", err)
	}
}

func TestValidateScanPath_Empty(t *testing.T) {
	err := ValidateScanPath("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestValidateScanPath_NotExist(t *testing.T) {
	err := ValidateScanPath("/nonexistent/path/xyz123")
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestValidateScanPath_NotDirectory(t *testing.T) {
	f, err := os.CreateTemp("", "safepath-test-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	if err := ValidateScanPath(f.Name()); err == nil {
		t.Fatal("expected error for file path")
	}
}

func TestValidateScanPath_SensitiveSystem(t *testing.T) {
	for _, dir := range []string{"/etc", "/proc", "/sys", "/dev"} {
		err := ValidateScanPath(dir)
		if err == nil {
			t.Fatalf("expected error for sensitive path %s", dir)
		}
	}
}

func TestValidateOutputPath(t *testing.T) {
	baseDir := t.TempDir()

	tests := []struct {
		name    string
		file    string
		base    string
		wantErr bool
	}{
		{"valid subpath", filepath.Join(baseDir, "snapshot.json"), baseDir, false},
		{"valid nested", filepath.Join(baseDir, "sub", "out.json"), baseDir, false},
		{"empty path", "", baseDir, true},
		{"dot-dot traversal", filepath.Join(baseDir, "..", "evil.json"), baseDir, true},
		{"absolute outside", "/tmp/evil.json", baseDir, true},
		{"sibling directory", baseDir + "attack/file.json", baseDir, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOutputPath(tt.file, tt.base)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateOutputPath(%q, %q) error = %v, wantErr %v", tt.file, tt.base, err, tt.wantErr)
			}
		})
	}
}

// TestValidateOutputPath_RejectsSymlinkEscape is a regression test for the
// Carlini-scaffold finding: ValidateOutputPath did `strings.HasPrefix(absFile,
// absBase+sep)` and never resolved symlinks. A symlink at <baseDir>/link
// pointing at <outside>/file lexically passed (since absFile stayed under
// absBase) but the actual write went outside.
func TestValidateOutputPath_RejectsSymlinkEscape(t *testing.T) {
	baseDir := t.TempDir()
	outsideDir := t.TempDir()

	link := filepath.Join(baseDir, "trapdoor")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// A write to baseDir/trapdoor/secret.json would land in outsideDir,
	// outside the allowed root.
	target := filepath.Join(link, "secret.json")
	if err := ValidateOutputPath(target, baseDir); err == nil {
		t.Fatal("expected rejection: write through baseDir-internal symlink escapes baseDir")
	}
}

// TestValidateOutputPath_RejectsHasPrefixSiblingTrick is a regression test for
// the same finding's other sub-pattern: HasPrefix-based containment treated
// `/tmp/baseEvil/x` as inside `/tmp/base` because the absBase string was a
// prefix of the absFile string. filepath.Rel rejects this.
func TestValidateOutputPath_RejectsHasPrefixSiblingTrick(t *testing.T) {
	parent := t.TempDir()
	base := filepath.Join(parent, "base")
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	siblingPrefix := filepath.Join(parent, "baseEvil")
	if err := os.MkdirAll(siblingPrefix, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(siblingPrefix, "out.json")
	if err := ValidateOutputPath(target, base); err == nil {
		t.Fatalf("expected rejection: %s is sibling of %s, not under it", target, base)
	}
}

func TestValidateScanPath_AllowlistPermitsInside(t *testing.T) {
	allowed := t.TempDir()
	t.Setenv(AllowedDirsEnv, allowed)

	// The allowlisted root itself is a valid scan target.
	if err := ValidateScanPath(allowed); err != nil {
		t.Fatalf("scan of allowlisted root should pass: %v", err)
	}

	// A directory nested under the allowlisted root is also valid.
	sub := filepath.Join(allowed, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateScanPath(sub); err != nil {
		t.Fatalf("scan of dir under allowlist should pass: %v", err)
	}
}

func TestValidateScanPath_AllowlistRefusesOutside(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	t.Setenv(AllowedDirsEnv, allowed)

	if err := ValidateScanPath(outside); err == nil {
		t.Fatal("expected refusal: scan target outside allowlist")
	}
}

func TestValidateScanPath_AllowlistMultipleDirs(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	outside := t.TempDir()
	t.Setenv(AllowedDirsEnv, a+string(os.PathListSeparator)+b)

	if err := ValidateScanPath(a); err != nil {
		t.Fatalf("first allowlisted dir should pass: %v", err)
	}
	if err := ValidateScanPath(b); err != nil {
		t.Fatalf("second allowlisted dir should pass: %v", err)
	}
	if err := ValidateScanPath(outside); err == nil {
		t.Fatal("expected refusal: dir outside a multi-entry allowlist")
	}
}

func TestValidateScanPath_AllowlistUnsetPermits(t *testing.T) {
	// Explicitly blank: falls back to denylist-only, so an ordinary temp dir
	// remains scannable.
	t.Setenv(AllowedDirsEnv, "")
	dir := t.TempDir()
	if err := ValidateScanPath(dir); err != nil {
		t.Fatalf("unset allowlist should permit an ordinary dir: %v", err)
	}
}

// TestValidateScanPath_AllowlistRefusesSymlinkEscape is the allowlist analogue
// of the ValidateOutputPath symlink-escape regression: a symlink placed at or
// under an allowlisted root must not launder a scan of a directory outside the
// allowlist. Containment is checked on the symlink-resolved target.
func TestValidateScanPath_AllowlistRefusesSymlinkEscape(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()

	secret := filepath.Join(outside, "secret")
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(allowed, "trapdoor")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	t.Setenv(AllowedDirsEnv, allowed)

	// allowed/trapdoor resolves to outside/secret, which is not under the
	// allowlist, so the scan must be refused.
	if err := ValidateScanPath(link); err == nil {
		t.Fatal("expected refusal: symlink under allowlist escapes to an outside dir")
	}
}

func TestValidateScanPath_SensitiveDotDirs(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	for _, dotDir := range sensitiveDotDirs {
		sensitive := filepath.Join(home, dotDir)
		// Only test if the directory exists
		if _, err := os.Stat(sensitive); err != nil {
			continue
		}
		if err := ValidateScanPath(sensitive); err == nil {
			t.Fatalf("expected error for sensitive path %s", sensitive)
		}
	}
}
