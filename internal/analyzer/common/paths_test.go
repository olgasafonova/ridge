package common

import (
	"path/filepath"
	"testing"
)

func TestRelToRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	tests := []struct {
		name, root, path, want string
		ok                     bool
	}{
		{"inside", root, filepath.Join(root, "src", "A.java"), "src/A.java", true},
		{"no root", "", filepath.Join(root, "src", "A.java"), "", false},
		{"outside", root, filepath.Join(string(filepath.Separator), "other", "A.java"), "", false},
		{"parent itself", filepath.Join(root, "src"), root, "", false},
		{"relative path against absolute root", root, "A.java", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := RelToRoot(tt.root, tt.path)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("RelToRoot(%q, %q) = %q, %v; want %q, %v", tt.root, tt.path, got, ok, tt.want, tt.ok)
			}
		})
	}
}
