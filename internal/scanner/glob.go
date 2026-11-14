package scanner

import (
	"path"
	"strings"
)

// Skip patterns come in two shapes:
//
//   - A pattern without a slash ("*_test.go") is matched against the file
//     name, so it applies in every folder.
//   - A pattern with a slash ("**/generated/**", "docs/*.md") is matched
//     against the path relative to the scan root, segment by segment. "**"
//     stands for any number of folders, including none.
//
// Within a segment the syntax is that of path.Match: "*", "?" and "[a-z]".

// doubleStar is the segment that matches any number of path segments.
const doubleStar = "**"

// matchesGlob reports whether pattern matches a file, given its name and its
// slash-separated path relative to the scan root.
func matchesGlob(pattern, baseName, relPath string) bool {
	pattern = strings.TrimPrefix(pattern, "./")
	if !strings.Contains(pattern, "/") {
		return matchSegment(pattern, baseName)
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(relPath, "/"))
}

// matchSegments matches pattern segments against path segments.
func matchSegments(pattern, segments []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == doubleStar {
			return matchDoubleStar(pattern[1:], segments)
		}
		if len(segments) == 0 || !matchSegment(pattern[0], segments[0]) {
			return false
		}
		pattern, segments = pattern[1:], segments[1:]
	}
	return len(segments) == 0
}

// matchDoubleStar matches the rest of a pattern after a "**" against every
// tail of the path. A trailing "**" matches whatever is left.
func matchDoubleStar(rest, segments []string) bool {
	if len(rest) == 0 {
		return true
	}
	for skip := 0; skip <= len(segments); skip++ {
		if matchSegments(rest, segments[skip:]) {
			return true
		}
	}
	return false
}

// matchSegment matches one pattern segment against one name. A malformed
// pattern matches nothing.
func matchSegment(pattern, name string) bool {
	matched, err := path.Match(pattern, name)
	return err == nil && matched
}
