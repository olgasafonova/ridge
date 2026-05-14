package tools

import (
	"os"
	"path/filepath"
	"testing"
)

const validRulesYAML = `rules:
  - name: forbid-handler-touches-db
    type: no_dependency
    severity: high
    from:
      type: service
    to:
      type: database
`

// TestLoadCustomRules_RefusedByDefault verifies that an in-repo
// .arch-rules.yaml is ignored unless the opt-in env var is set.
// Threat model: an attacker who controls the analyzed content could
// otherwise ship rules that downgrade their own violations.
func TestLoadCustomRules_RefusedByDefault(t *testing.T) {
	repo := t.TempDir()
	rulesPath := filepath.Join(repo, ".arch-rules.yaml")
	if err := os.WriteFile(rulesPath, []byte(validRulesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(inRepoRulesEnv, "")

	h := NewHandlerRegistry(testLogger())
	rules := h.loadCustomRules(repo)

	if rules != nil {
		t.Fatalf("expected nil rules when %s is unset, got %+v", inRepoRulesEnv, rules)
	}
}

// TestLoadCustomRules_LoadedWithOptIn verifies that the env var = "1"
// gates the load, and the parsed rules round-trip.
func TestLoadCustomRules_LoadedWithOptIn(t *testing.T) {
	repo := t.TempDir()
	rulesPath := filepath.Join(repo, ".arch-rules.yaml")
	if err := os.WriteFile(rulesPath, []byte(validRulesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(inRepoRulesEnv, "1")

	h := NewHandlerRegistry(testLogger())
	rules := h.loadCustomRules(repo)

	if rules == nil {
		t.Fatal("expected rules to load when env=1, got nil")
	}
	if len(rules.Rules) != 1 || rules.Rules[0].Name != "forbid-handler-touches-db" {
		t.Fatalf("unexpected parsed rules: %+v", rules.Rules)
	}
}

// TestLoadCustomRules_NoFile verifies that a missing rules file returns
// nil without warning, regardless of the env var.
func TestLoadCustomRules_NoFile(t *testing.T) {
	repo := t.TempDir()
	t.Setenv(inRepoRulesEnv, "1")

	h := NewHandlerRegistry(testLogger())
	rules := h.loadCustomRules(repo)

	if rules != nil {
		t.Fatalf("expected nil rules when file absent, got %+v", rules)
	}
}

// TestLoadCustomRules_OptInRejectsNonOne verifies that values other than
// exactly "1" do not opt in (e.g., "true", "yes", "0" all stay refused).
func TestLoadCustomRules_OptInRejectsNonOne(t *testing.T) {
	repo := t.TempDir()
	rulesPath := filepath.Join(repo, ".arch-rules.yaml")
	if err := os.WriteFile(rulesPath, []byte(validRulesYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, v := range []string{"true", "yes", "0", "2", " 1"} {
		t.Run("env="+v, func(t *testing.T) {
			t.Setenv(inRepoRulesEnv, v)
			h := NewHandlerRegistry(testLogger())
			if got := h.loadCustomRules(repo); got != nil {
				t.Fatalf("expected nil rules for env=%q, got %+v", v, got)
			}
		})
	}
}
