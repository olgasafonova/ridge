package scanner

import (
	"crypto/sha256"
	"fmt"
	"os"
	"time"

	"github.com/olgasafonova/ridge/internal/model"
)

// FileEntry tracks a single file's state and cached analysis results.
type FileEntry struct {
	Path        string    `json:"path"`
	ModTime     time.Time `json:"mod_time"`
	ContentHash string    `json:"content_hash"`
	// AnalyzerSignature records which analyzer version produced the cached
	// Nodes/Edges. Entries with a mismatching signature must be re-analyzed
	// even when content is unchanged. Empty on legacy state files (pre-v2)
	// triggers a one-time full rescan on first run after upgrade.
	AnalyzerSignature string        `json:"analyzer_signature,omitempty"`
	Nodes             []*model.Node `json:"nodes,omitempty"`
	Edges             []*model.Edge `json:"edges,omitempty"`
}

// ScanState holds the incremental scan state for a codebase.
type ScanState struct {
	Version  string                `json:"version"`
	RootPath string                `json:"root_path"`
	Files    map[string]*FileEntry `json:"files"`
	LastScan time.Time             `json:"last_scan"`
}

// NewScanState creates an empty scan state for the given root.
func NewScanState(rootPath string) *ScanState {
	return &ScanState{
		Version:  "2",
		RootPath: rootPath,
		Files:    make(map[string]*FileEntry),
	}
}

// FileChange classifies what happened to a file.
type FileChange int

const (
	FileUnchanged FileChange = iota
	FileAdded
	FileModified
	FileDeleted
)

// ChangeSet holds classified file changes from a scan.
type ChangeSet struct {
	Unchanged []string // paths with no changes (use cached results)
	Added     []string // new files to analyze
	Modified  []string // changed files to re-analyze
	Deleted   []string // removed files (drop cached results)
}

// DetectChanges compares walked files against the saved state.
// walkedFiles is the set of file paths discovered during directory walking.
func (s *ScanState) DetectChanges(walkedFiles []string) (*ChangeSet, error) {
	cs := &ChangeSet{}
	seen := make(map[string]bool, len(walkedFiles))

	for _, path := range walkedFiles {
		seen[path] = true
		prev, exists := s.Files[path]

		if !exists {
			cs.Added = append(cs.Added, path)
			continue
		}

		// Fast path: check mtime first (stat is cheap, hash is not)
		info, err := os.Stat(path)
		if err != nil {
			cs.Added = append(cs.Added, path) // treat stat errors as new
			continue
		}

		if info.ModTime().Equal(prev.ModTime) {
			cs.Unchanged = append(cs.Unchanged, path)
			continue
		}

		// mtime changed; check content hash to distinguish real changes from touches
		hash, err := hashFile(path)
		if err != nil {
			cs.Modified = append(cs.Modified, path) // conservative: re-analyze on hash error
			continue
		}

		if hash == prev.ContentHash {
			// File was touched but content is the same; update mtime only
			prev.ModTime = info.ModTime()
			cs.Unchanged = append(cs.Unchanged, path)
			continue
		}

		cs.Modified = append(cs.Modified, path)
	}

	// Detect deletions: files in state but not in walked set
	for path := range s.Files {
		if !seen[path] {
			cs.Deleted = append(cs.Deleted, path)
		}
	}

	return cs, nil
}

// UpdateFile records analysis results for a file. The analyzerSig identifies
// which analyzer version produced these results so the cache can be invalidated
// when the analyzer's behavior changes (pass "" only from tests that don't
// exercise signature-based invalidation).
func (s *ScanState) UpdateFile(path string, analyzerSig string, nodes []*model.Node, edges []*model.Edge) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	hash, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}

	s.Files[path] = &FileEntry{
		Path:              path,
		ModTime:           info.ModTime(),
		ContentHash:       hash,
		AnalyzerSignature: analyzerSig,
		Nodes:             nodes,
		Edges:             edges,
	}

	return nil
}

// RemoveFile drops a file from the state.
func (s *ScanState) RemoveFile(path string) {
	delete(s.Files, path)
}

// CachedResult returns the cached nodes, edges, and analyzer signature for an
// unchanged file. The signature lets callers verify the cached results were
// produced by an analyzer with matching behavior before reusing them.
func (s *ScanState) CachedResult(path string) (sig string, nodes []*model.Node, edges []*model.Edge, ok bool) {
	entry, exists := s.Files[path]
	if !exists {
		return "", nil, nil, false
	}
	return entry.AnalyzerSignature, entry.Nodes, entry.Edges, true
}

// hashFile computes the SHA-256 hex digest of a file's contents.
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h), nil
}
