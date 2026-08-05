package tools

import (
	"strings"
	"testing"
)

func TestAllToolsHaveRequiredFields(t *testing.T) {
	for _, spec := range AllTools {
		if spec.Name == "" {
			t.Errorf("tool missing Name")
		}
		if spec.Method == "" {
			t.Errorf("tool %q missing Method", spec.Name)
		}
		if spec.Title == "" {
			t.Errorf("tool %q missing Title", spec.Name)
		}
		if spec.Description == "" {
			t.Errorf("tool %q missing Description", spec.Name)
		}
		if spec.Category == "" {
			t.Errorf("tool %q missing Category", spec.Name)
		}
	}
}

func TestAllToolsHaveExpectedCount(t *testing.T) {
	if len(AllTools) != 19 {
		t.Fatalf("expected 19 tools, got %d", len(AllTools))
	}
}

func TestAllToolDescriptionsHaveUSEWHEN(t *testing.T) {
	for _, spec := range AllTools {
		if !strings.Contains(spec.Description, "USE WHEN") {
			t.Errorf("tool %q description missing 'USE WHEN' pattern", spec.Name)
		}
	}
}

func TestToolNamesAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, spec := range AllTools {
		if seen[spec.Name] {
			t.Errorf("duplicate tool name: %q", spec.Name)
		}
		seen[spec.Name] = true
	}
}

func TestIsStdlib(t *testing.T) {
	tests := []struct {
		input      string
		modulePath string
		want       bool
	}{
		{"fmt", "", true},
		{"net/http", "", true},
		{"encoding/json", "", true},
		{"golang.org/x/crypto", "", true},  // extended stdlib
		{"golang.org/x/text", "", true},    // extended stdlib
		{"github.com/user/pkg", "", false}, // external
		{"gorm.io/gorm", "", false},        // external
		{"mycompany.com/svc", "", false},   // external
		// Module-path awareness
		{"github.com/user/repo/internal/model", "github.com/user/repo", false}, // module-internal
		{"github.com/user/repo/pkg/util", "github.com/user/repo", false},       // module-internal
		{"internal/model", "", true},                                           // no go.mod fallback: treated as stdlib
		{"github.com/other/repo", "github.com/user/repo", false},               // external (different module)
	}
	for _, tt := range tests {
		got := Module{Path: tt.modulePath}.IsStdlib(tt.input)
		if got != tt.want {
			t.Errorf("Module{%q}.IsStdlib(%q) = %v, want %v", tt.modulePath, tt.input, got, tt.want)
		}
	}
}

func TestToolMethodsAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, spec := range AllTools {
		if seen[spec.Method] {
			t.Errorf("duplicate tool method: %q", spec.Method)
		}
		seen[spec.Method] = true
	}
}

// ptr backs the optional OpenWorldHint annotation in register()
// (handlers.go), where *bool distinguishes "unset" from "false". The
// contract that matters is that each call returns a pointer to its own
// copy, so no two annotations alias the same bool.
func TestPtrReturnsDistinctCopies(t *testing.T) {
	a, b := ptr(true), ptr(true)
	if a == nil || *a != true {
		t.Fatalf("ptr(true) = %v, want pointer to true", a)
	}
	if a == b {
		t.Error("ptr(true) returned the same pointer twice; each call must copy")
	}
	*a = false
	if !*b {
		t.Error("mutating one ptr result changed another; values must not alias")
	}
}
