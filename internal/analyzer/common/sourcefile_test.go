package common

import (
	"path/filepath"
	"testing"
)

func TestSourceFile_IDs(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	path := filepath.Join(root, "billing", "app", "views.py")
	outside := filepath.Join(string(filepath.Separator), "other", "app", "views.py")

	tests := []struct {
		name             string
		file             SourceFile
		wantMod, wantEnd string
	}{
		{"rooted", SourceFile{Root: root, Path: path}, "mod:billing/app/views", "endpoint:billing/app/views.py:7"},
		{"no root", SourceFile{Path: path}, "mod:app/views", "endpoint:views.py:7"},
		{"outside root", SourceFile{Root: root, Path: outside}, "mod:app/views", "endpoint:views.py:7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.file.ModuleID(); got != tt.wantMod {
				t.Errorf("ModuleID = %q, want %q", got, tt.wantMod)
			}
			if got := tt.file.EndpointID(7); got != tt.wantEnd {
				t.Errorf("EndpointID = %q, want %q", got, tt.wantEnd)
			}
			if got := tt.file.ModuleName(); got != "views" {
				t.Errorf("ModuleName = %q, want views", got)
			}
		})
	}
}
