package scanner

import (
	"context"
	"io/fs"
	"maps"
	"path/filepath"
	"strings"
)

// fileWork represents a file to be analyzed.
type fileWork struct {
	path string
	ext  string
}

// mergeSkipDirs combines the scanner's defaults with any extras from opts,
// returning a fresh map so the scanner's own skipDirs is never mutated.
func (s *Scanner) mergeSkipDirs(extra []string) map[string]bool {
	merged := make(map[string]bool, len(s.skipDirs)+len(extra))
	maps.Copy(merged, s.skipDirs)
	for _, d := range extra {
		merged[d] = true
	}
	return merged
}

// walkAndCollect performs Phase 1: a single-threaded WalkDir that produces the
// file work list, applying skip-dirs, skip-globs, extension filtering, and the
// MaxFiles cap. Truncation is true if context cancellation or MaxFiles ended
// the walk early.
func (s *Scanner) walkAndCollect(ctx context.Context, absRoot string, skipDirs map[string]bool, skipGlobs []string, maxFiles int) (files []fileWork, skipped int, truncated bool, err error) {
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if ctx.Err() != nil {
			truncated = true
			return filepath.SkipAll
		}
		if maxFiles > 0 && len(files) >= maxFiles {
			truncated = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip symlinks. WalkDir surfaces the symlink itself (not the
		// target) here; analyzers later os.ReadFile the path, which
		// follows the link. A symlink under absRoot can point at any
		// readable file on the system — verified PoC reads /etc/passwd via
		// `innocent.py -> /etc/passwd`. Default-deny is safe; users who
		// genuinely need symlink-following can opt in via a future flag.
		if d.Type()&fs.ModeSymlink != 0 {
			skipped++
			return nil
		}
		if matchesAnyGlob(skipGlobs, d.Name(), path, absRoot) {
			skipped++
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if _, ok := s.analyzers[ext]; !ok {
			return nil
		}
		files = append(files, fileWork{path: path, ext: ext})
		return nil
	})
	if ctx.Err() != nil {
		truncated = true
	}
	return files, skipped, truncated, err
}

// matchesAnyGlob returns true if name or relative path matches any of the patterns.
func matchesAnyGlob(patterns []string, baseName, fullPath, absRoot string) bool {
	if len(patterns) == 0 {
		return false
	}
	relPath, _ := filepath.Rel(absRoot, fullPath)
	for _, pattern := range patterns {
		if matched, _ := filepath.Match(pattern, baseName); matched {
			return true
		}
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return true
		}
	}
	return false
}
