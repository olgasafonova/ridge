package scanner

import "testing"

func TestMatchesGlob(t *testing.T) {
	tests := []struct {
		pattern string
		relPath string
		want    bool
	}{
		// No slash: the file name, in any folder.
		{"*_test.go", "main_test.go", true},
		{"*_test.go", "internal/deep/main_test.go", true},
		{"*_test.go", "main.go", false},
		{"*.md", "docs/notes/a.md", true},

		// "**/" matches any number of folders, including none.
		{"**/*.md", "README.md", true},
		{"**/*.md", "docs/a.md", true},
		{"**/*.md", "docs/notes/deep/a.md", true},
		{"**/*.md", "docs/a.go", false},
		{"**/*_test.go", "internal/scanner/glob_test.go", true},

		// A folder anywhere in the tree, and everything below it.
		{"**/generated/**", "generated/a.go", true},
		{"**/generated/**", "src/generated/api/client.ts", true},
		{"**/generated/**", "src/generator/a.go", false},
		{"**/generated/**", "src/generated.go", false},

		// Anchored at the scan root.
		{"docs/*.md", "docs/a.md", true},
		{"docs/*.md", "docs/notes/a.md", false},
		{"docs/*.md", "src/docs/a.md", false},
		{"docs/**", "docs/notes/a.md", true},
		{"docs/**/*.md", "docs/a.md", true},
		{"src/**/fixtures/*.ts", "src/a/b/fixtures/x.ts", true},
		{"src/**/fixtures/*.ts", "src/a/b/fixtures/deep/x.ts", false},
		{"./docs/*.md", "docs/a.md", true},

		// Longer than the path, and malformed.
		{"a/b/c/*.go", "a/b.go", false},
		{"[", "a.go", false},
		{"**/[", "src/a.go", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+" on "+tt.relPath, func(t *testing.T) {
			base := tt.relPath
			for i := len(base) - 1; i >= 0; i-- {
				if base[i] == '/' {
					base = base[i+1:]
					break
				}
			}
			if got := matchesGlob(tt.pattern, base, tt.relPath); got != tt.want {
				t.Fatalf("matchesGlob(%q, %q) = %v, want %v", tt.pattern, tt.relPath, got, tt.want)
			}
		})
	}
}
