// Package safepath validates user-supplied filesystem paths for scan operations.
package safepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sensitiveRoots are directories that should never be scanned.
var sensitiveRoots = []string{
	"/etc",
	"/proc",
	"/sys",
	"/dev",
}

// sensitiveDotDirs are home-directory dotfiles that should never be scanned.
var sensitiveDotDirs = []string{
	".ssh",
	".gnupg",
	".aws",
	".config/gcloud",
}

// AllowedDirsEnv names the environment variable that opts the server into an
// allowlist for scan roots. Its value is a PATH-style list of absolute
// directory paths (colon-separated on Unix, os.PathListSeparator generally).
// When set, ValidateScanPath refuses any scan whose symlink-resolved target is
// not at or under one of the listed directories. When unset, scans fall back to
// the sensitive-root/dotdir denylist only.
const AllowedDirsEnv = "RIDGE_ALLOWED_DIRS"

// AllowedDirs parses AllowedDirsEnv into a list of symlink-resolved absolute
// directories. The bool reports whether an allowlist is configured at all: it
// is true whenever the env var holds any non-whitespace content, even if every
// entry failed to resolve. That makes a set-but-unusable allowlist fail closed
// (nothing matches an empty dir list, so every scan is refused) rather than
// silently reverting to permit-all. When the env var is unset or blank the bool
// is false and the caller should apply denylist-only behavior.
func AllowedDirs() ([]string, bool) {
	raw := os.Getenv(AllowedDirsEnv)
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var dirs []string
	for _, entry := range strings.Split(raw, string(os.PathListSeparator)) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		abs, err := filepath.Abs(entry)
		if err != nil {
			continue
		}
		// Resolve symlinks so containment compares what the kernel opens, not
		// the lexical path (the HG-5 idiom, applied to the allowlist side too).
		if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
			dirs = append(dirs, resolved)
		} else {
			dirs = append(dirs, filepath.Clean(abs))
		}
	}
	return dirs, true
}

// ValidateScanPath checks that a path is safe for scanning.
// Returns an error if the path is empty, doesn't exist, is not a directory,
// or points to a sensitive location.
func ValidateScanPath(path string) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	pathsToCheck := bothPaths(absPath)

	if hit := matchSensitiveRoot(pathsToCheck); hit != "" {
		return fmt.Errorf("scanning %s is not allowed", hit)
	}
	if hit := matchSensitiveDotDir(pathsToCheck); hit != "" {
		return fmt.Errorf("scanning %s is not allowed", hit)
	}

	if err := requireDir(absPath); err != nil {
		return err
	}

	return checkAllowlist(absPath)
}

// requireDir verifies that absPath exists and is a directory.
func requireDir(absPath string) error {
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("path does not exist: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", absPath)
	}
	return nil
}

// checkAllowlist enforces the RIDGE_ALLOWED_DIRS allowlist when configured.
// The scan path's symlinks are resolved before the containment check so a
// symlink at or under an allowed dir that points outside it is refused, and so
// t.TempDir()-style paths (/var -> /private/var on macOS) match consistently
// against the already-resolved allowlist entries.
func checkAllowlist(absPath string) error {
	dirs, ok := AllowedDirs()
	if !ok {
		return nil
	}
	resolvedScan := absPath
	if r, rerr := filepath.EvalSymlinks(absPath); rerr == nil {
		resolvedScan = r
	}
	if !anyContains(dirs, resolvedScan) {
		return fmt.Errorf("scanning %s is not allowed: path is outside the %s allowlist", absPath, AllowedDirsEnv)
	}
	return nil
}

// anyContains reports whether resolvedPath equals or sits under any of dirs.
func anyContains(dirs []string, resolvedPath string) bool {
	for _, d := range dirs {
		if pathIsAtOrUnder(resolvedPath, d) {
			return true
		}
	}
	return false
}

// bothPaths returns absPath plus its symlink-resolved form so sensitivity
// checks see what the kernel will actually open. EvalSymlinks failure is
// non-fatal here because non-existent paths fail more clearly at the later
// os.Stat check.
func bothPaths(absPath string) []string {
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return []string{absPath}
	}
	return []string{absPath, resolved}
}

// matchSensitiveRoot returns the first sensitiveRoots entry that any of paths
// resides at or under, or "" if none.
func matchSensitiveRoot(paths []string) string {
	for _, p := range paths {
		for _, root := range sensitiveRoots {
			if pathIsAtOrUnder(p, root) {
				return root
			}
		}
	}
	return ""
}

// matchSensitiveDotDir returns the absolute path of the first $HOME/<dotdir>
// entry that any of paths resides at or under, or "" if none / no $HOME.
func matchSensitiveDotDir(paths []string) string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	for _, p := range paths {
		for _, dotDir := range sensitiveDotDirs {
			sensitive := filepath.Join(home, dotDir)
			if pathIsAtOrUnder(p, sensitive) {
				return sensitive
			}
		}
	}
	return ""
}

// pathIsAtOrUnder reports whether p equals root or sits inside it (with a
// trailing separator to defeat sibling-prefix tricks like "/etc-evil").
func pathIsAtOrUnder(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// ValidateOutputPath checks that a file path resolves to within baseDir.
// Unlike ValidateScanPath, the file does not need to exist yet (it may be
// created). The check uses filepath.Rel containment instead of HasPrefix
// (which is bypassable by sibling-prefix tricks like
// /tmp/baseEvil vs /tmp/base) and resolves symlinks on baseDir AND on
// the deepest existing ancestor of filePath, so a symlink under baseDir
// can't redirect the write outside it.
//
// Returns an error if the path escapes baseDir, targets a sensitive root,
// or fails resolution.
func ValidateOutputPath(filePath, baseDir string) error {
	if filePath == "" {
		return fmt.Errorf("file path is required")
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("resolving base directory: %w", err)
	}
	absBase = filepath.Clean(absBase)

	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("resolving file path: %w", err)
	}
	absFile = filepath.Clean(absFile)

	// Resolve symlinks on baseDir. baseDir is expected to exist; if it
	// doesn't, EvalSymlinks fails and we surface the error rather than
	// silently allowing.
	resolvedBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		return fmt.Errorf("resolving base directory symlinks: %w", err)
	}

	// Resolve symlinks on the deepest existing ancestor of filePath. The
	// file itself may not exist yet (this is a write check), but any
	// existing ancestor directory could be a symlink that redirects the
	// write outside baseDir.
	resolvedFile, err := evalDeepestAncestor(absFile)
	if err != nil {
		return fmt.Errorf("resolving file path symlinks: %w", err)
	}

	// Lexical Rel containment on the resolved values. Catches both `..`
	// escapes and sibling-prefix tricks that HasPrefix would have missed.
	if err := containedIn(resolvedFile, resolvedBase); err != nil {
		return err
	}

	// Defense in depth: also reject writes to sensitive roots even if
	// baseDir overlapped them.
	if hit := matchSensitiveRoot([]string{resolvedFile}); hit != "" {
		return fmt.Errorf("writing to %s is not allowed", hit)
	}

	return nil
}

// evalDeepestAncestor returns the path with the deepest existing ancestor
// directory's symlinks resolved. The basename (and any non-existent
// intermediate components) is appended unchanged. This lets ValidateOutputPath
// accept paths to files that do not yet exist while still defeating
// symlinked-ancestor tricks.
func evalDeepestAncestor(p string) (string, error) {
	cur := p
	suffix := ""
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			if suffix == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, suffix), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached the filesystem root and nothing existed; treat the
			// original path as already-canonical.
			return p, nil
		}
		base := filepath.Base(cur)
		if suffix == "" {
			suffix = base
		} else {
			suffix = filepath.Join(base, suffix)
		}
		cur = parent
	}
}

func containedIn(file, base string) error {
	rel, err := filepath.Rel(base, file)
	if err != nil {
		return fmt.Errorf("file path %s is outside allowed directory %s", file, base)
	}
	if rel == "." {
		// `file == base` is not a valid output path.
		return fmt.Errorf("file path equals base directory %s", base)
	}
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("file path %s is outside allowed directory %s", file, base)
	}
	return nil
}
