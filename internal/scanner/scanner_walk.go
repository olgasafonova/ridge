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

// walkParams bundles the per-walk inputs for walkAndCollect. Carrying these on
// a struct keeps the function signature at three arguments and lets the inner
// callback close over a single value.
type walkParams struct {
	absRoot   string
	skipDirs  map[string]bool
	skipGlobs []string
	maxFiles  int
}

// walkDecision is the result of classifying a single dir-entry during the walk.
type walkDecision int

const (
	walkAccept   walkDecision = iota // include this file in fileWork
	walkSkipFile                     // ignore (no count change)
	walkSkipDir                      // skip the entire directory subtree
	walkCount                        // skipped, increment skipped counter
)

// walkAndCollect performs Phase 1: a single-threaded WalkDir that produces the
// file work list, applying skip-dirs, skip-globs, extension filtering, and the
// MaxFiles cap. Truncation is true if context cancellation or MaxFiles ended
// the walk early.
func (s *Scanner) walkAndCollect(ctx context.Context, p walkParams) (files []fileWork, skipped int, truncated bool, err error) {
	err = filepath.WalkDir(p.absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if shouldStopWalk(ctx, len(files), p.maxFiles) {
			truncated = true
			return filepath.SkipAll
		}

		decision, ext := s.classifyWalkEntry(path, d, p)
		switch decision {
		case walkSkipDir:
			return filepath.SkipDir
		case walkCount:
			skipped++
		case walkAccept:
			files = append(files, fileWork{path: path, ext: ext})
		}
		return nil
	})
	if ctx.Err() != nil {
		truncated = true
	}
	return files, skipped, truncated, err
}

// shouldStopWalk reports whether the walk should bail out early because the
// context was cancelled or the file cap has been reached.
func shouldStopWalk(ctx context.Context, collected, maxFiles int) bool {
	if ctx.Err() != nil {
		return true
	}
	return maxFiles > 0 && collected >= maxFiles
}

// classifyWalkEntry returns the action to take for a single dir-entry plus the
// resolved extension when accepting a file. Pulled out of walkAndCollect to
// keep the WalkDir callback flat.
//
// Symlinks are skipped: WalkDir surfaces the symlink (not the target), and a
// later os.ReadFile follows the link. An attacker-placed `innocent.py ->
// /etc/passwd` would otherwise be read. Default-deny; opt-in could come later
// behind a flag.
func (s *Scanner) classifyWalkEntry(path string, d fs.DirEntry, p walkParams) (walkDecision, string) {
	if d.IsDir() {
		if p.skipDirs[d.Name()] {
			return walkSkipDir, ""
		}
		return walkSkipFile, ""
	}
	if d.Type()&fs.ModeSymlink != 0 {
		return walkCount, ""
	}
	if matchesAnyGlob(p.skipGlobs, d.Name(), path, p.absRoot) {
		return walkCount, ""
	}
	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := s.analyzers[ext]; !ok {
		return walkSkipFile, ""
	}
	return walkAccept, ext
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
