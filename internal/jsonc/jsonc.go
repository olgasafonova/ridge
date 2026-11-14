// Package jsonc converts JSON-with-comments, the dialect tsconfig.json and
// similar tool configs are written in, into plain JSON that encoding/json
// can parse.
package jsonc

import "bytes"

// Strip removes // and /* */ comments plus trailing commas from JSONC,
// preserving string contents, so encoding/json can parse tsconfig files.
// Two passes: comments first, then trailing commas — a single pass would
// miss a trailing comma separated from its closing brace by a comment
// (`"x", /* note */ }`), because the comma lookahead would see the comment.
func Strip(data []byte) []byte {
	return stripTrailingCommas(stripComments(data))
}

// stripComments removes // and /* */ comments outside string literals.
func stripComments(data []byte) []byte {
	s := newJSONCScanner(data)
	for !s.done() {
		switch {
		case s.peek() == '"':
			s.copyString()
		case s.atComment('/'):
			s.skipLineComment()
		case s.atComment('*'):
			s.skipBlockComment()
		default:
			s.copyByte()
		}
	}
	return s.out
}

// stripTrailingCommas removes commas whose next non-whitespace byte closes
// a container, outside string literals.
func stripTrailingCommas(data []byte) []byte {
	s := newJSONCScanner(data)
	for !s.done() {
		switch {
		case s.peek() == '"':
			s.copyString()
		case s.peek() == ',' && s.nextClosesContainer():
			s.pos++ // trailing comma: drop it
		default:
			s.copyByte()
		}
	}
	return s.out
}

// jsoncScanner walks JSONC input emitting cleaned output, in the shape of a
// hand-rolled lexer: pos advances over data, out accumulates kept bytes.
type jsoncScanner struct {
	data []byte
	pos  int
	out  []byte
}

func newJSONCScanner(data []byte) *jsoncScanner {
	return &jsoncScanner{data: data, out: make([]byte, 0, len(data))}
}

func (s *jsoncScanner) done() bool { return s.pos >= len(s.data) }

func (s *jsoncScanner) peek() byte { return s.data[s.pos] }

// copyByte keeps the current byte and advances.
func (s *jsoncScanner) copyByte() {
	s.out = append(s.out, s.data[s.pos])
	s.pos++
}

// copyString keeps the string literal starting at the current opening quote,
// through its closing quote. Escaped characters, including \", are copied
// verbatim.
func (s *jsoncScanner) copyString() {
	s.copyByte() // opening quote
	for !s.done() {
		ch := s.peek()
		s.copyByte()
		if ch == '\\' && !s.done() {
			s.copyByte()
			continue
		}
		if ch == '"' {
			return
		}
	}
}

// atComment reports whether the scanner sits on '/' followed by marker
// ('/' for line comments, '*' for block comments).
func (s *jsoncScanner) atComment(marker byte) bool {
	if s.peek() != '/' {
		return false
	}
	return s.pos+1 < len(s.data) && s.data[s.pos+1] == marker
}

// skipLineComment advances past a // comment, leaving the terminating
// newline (if any) for the main loop to copy.
func (s *jsoncScanner) skipLineComment() {
	if j := bytes.IndexByte(s.data[s.pos:], '\n'); j >= 0 {
		s.pos += j
		return
	}
	s.pos = len(s.data)
}

// skipBlockComment advances past a /* */ comment, including the closer.
// An unterminated comment consumes the rest of the input.
func (s *jsoncScanner) skipBlockComment() {
	if j := bytes.Index(s.data[s.pos+2:], []byte("*/")); j >= 0 {
		s.pos += 2 + j + 2
		return
	}
	s.pos = len(s.data)
}

// nextClosesContainer reports whether the next non-whitespace byte after the
// current one closes a JSON container, making a preceding comma trailing.
func (s *jsoncScanner) nextClosesContainer() bool {
	i := s.pos + 1
	for i < len(s.data) && isJSONSpace(s.data[i]) {
		i++
	}
	if i >= len(s.data) {
		return false
	}
	return s.data[i] == '}' || s.data[i] == ']'
}

func isJSONSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}
