package typescript

import "bytes"

// A Svelte component mixes markup, styles and one or two <script> blocks.
// Only the script blocks are TypeScript (or JavaScript, which the TypeScript
// parser also accepts), and they carry what this analyzer looks for: imports
// and HTTP calls. Markup and styles are not parsed.

var (
	scriptOpen  = []byte("<script")
	scriptClose = []byte("</script")
)

// svelteScriptSource returns src with everything outside its <script> blocks
// blanked out. Line breaks are kept and every other byte becomes a space, so
// the result has the same length and a node's line number in it is the line
// number in the component file.
func svelteScriptSource(src []byte) []byte {
	out := blanked(src)
	pos := 0
	for {
		start, end, ok := nextScriptBody(src, pos)
		if !ok {
			return out
		}
		copy(out[start:end], src[start:end])
		pos = end
	}
}

// blanked returns a copy of src with every byte but line breaks set to a space.
func blanked(src []byte) []byte {
	out := bytes.Repeat([]byte{' '}, len(src))
	for i, b := range src {
		if b == '\n' || b == '\r' {
			out[i] = b
		}
	}
	return out
}

// nextScriptBody finds the next <script> block at or after from and returns
// the byte range of its content.
func nextScriptBody(src []byte, from int) (start, end int, ok bool) {
	for from < len(src) {
		open := bytes.Index(src[from:], scriptOpen)
		if open < 0 {
			return 0, 0, false
		}
		open += from
		afterName := open + len(scriptOpen)
		if afterName < len(src) && isTagNameByte(src[afterName]) {
			from = afterName // "<scripted-thing>", not a script tag
			continue
		}
		start = tagEnd(src, afterName)
		if start < 0 {
			return 0, 0, false
		}
		closeAt := bytes.Index(src[start:], scriptClose)
		if closeAt < 0 {
			return 0, 0, false
		}
		return start, start + closeAt, true
	}
	return 0, 0, false
}

// tagEnd returns the index just past the ">" that closes the tag whose
// attributes start at from, or -1. A ">" inside a quoted attribute value
// (generics="T extends Record<string, U>") does not end the tag.
func tagEnd(src []byte, from int) int {
	var quote byte
	for i := from; i < len(src); i++ {
		c := src[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i + 1
		}
	}
	return -1
}

func isTagNameByte(b byte) bool {
	return b == '-' || b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
