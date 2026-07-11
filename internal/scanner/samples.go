package scanner

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/olgasafonova/ridge/internal/model"
)

// DefaultSampleLines is the number of source lines included per node when
// PopulateSamples is invoked without an explicit line count.
const DefaultSampleLines = 6

// maxSampleFileBytes caps the size of files read for sample extraction.
// Nodes pointing at oversized files get no sample rather than a partial read.
const maxSampleFileBytes = 2 * 1024 * 1024 // 2 MiB

// PopulateSamples fills Node.Source for nodes whose Path resolves to a single
// source file. Nodes with a "line" property start the snippet at that line;
// other file-backed nodes start at the first non-blank line. Directory-path
// nodes (packages, modules) are skipped because they do not pin to a single
// file. Read errors are silently skipped so a missing or unreadable file
// degrades to "no sample" rather than failing the scan. Every snippet passes
// through maskSecrets before it lands in Node.Source, so hardcoded
// credentials in scanned source never reach MCP callers.
func PopulateSamples(graph *model.ArchGraph, lines int) {
	if lines <= 0 {
		lines = DefaultSampleLines
	}
	cache := make(map[string][]string)
	for _, n := range graph.Nodes() {
		if n.Path == "" || n.Source != "" {
			continue
		}
		startLine, ok := sampleStartLine(n)
		if !ok {
			continue
		}
		fileLines, ok := readFileLines(n.Path, cache)
		if !ok {
			continue
		}
		snippet := extractSnippet(fileLines, startLine, lines)
		if snippet == "" {
			continue
		}
		n.Source = maskSnippet(snippet)
	}
}

// sampleStartLine returns the 1-based starting line for the node's sample.
// Returns ok=false if the node's Path isn't a regular file.
func sampleStartLine(n *model.Node) (int, bool) {
	info, err := os.Stat(n.Path)
	if err != nil || info.IsDir() {
		return 0, false
	}
	if lineStr, ok := n.Properties["line"]; ok {
		if line, convErr := strconv.Atoi(lineStr); convErr == nil && line > 0 {
			return line, true
		}
	}
	return 1, true
}

// readFileLines returns the file's lines, using cache for repeated reads.
// Files larger than maxSampleFileBytes return ok=false.
func readFileLines(path string, cache map[string][]string) ([]string, bool) {
	if cached, ok := cache[path]; ok {
		return cached, true
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxSampleFileBytes {
		cache[path] = nil
		return nil, false
	}
	f, err := os.Open(filepath.Clean(path)) // #nosec G304 -- path comes from scanned graph
	if err != nil {
		cache[path] = nil
		return nil, false
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if scanner.Err() != nil {
		cache[path] = nil
		return nil, false
	}
	cache[path] = lines
	return lines, true
}

// extractSnippet returns at most count lines starting at startLine (1-based).
// If startLine points past blank lines at the head of the slice, scans forward
// to the first non-blank line so the snippet leads with content.
func extractSnippet(lines []string, startLine, count int) string {
	if len(lines) == 0 || startLine < 1 {
		return ""
	}
	idx := startLine - 1
	if idx >= len(lines) {
		return ""
	}
	if startLine == 1 {
		for idx < len(lines) && strings.TrimSpace(lines[idx]) == "" {
			idx++
		}
		if idx >= len(lines) {
			return ""
		}
	}
	end := min(idx+count, len(lines))
	return strings.Join(lines[idx:end], "\n")
}
