package detector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/olgasafonova/ridge/internal/model"
)

// RulesConfig holds custom architecture validation rules loaded from .arch-rules.yaml.
type RulesConfig struct {
	Rules []Rule `yaml:"rules"`
}

// Rule defines a single architecture constraint.
type Rule struct {
	Name        string  `yaml:"name"`
	Description string  `yaml:"description"`
	Type        string  `yaml:"type"`     // "no_dependency" or "require_dependency"
	Severity    string  `yaml:"severity"` // "critical", "high", "medium", "low"
	From        Matcher `yaml:"from"`
	To          Matcher `yaml:"to"`
}

// Matcher filters nodes by type and/or path glob.
type Matcher struct {
	Type string `yaml:"type,omitempty"` // node type (endpoint, database, service, module, etc.)
	Path string `yaml:"path,omitempty"` // glob pattern on relative node path
}

// LoadRules reads and parses an .arch-rules.yaml file.
func LoadRules(path string) (*RulesConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading rules file: %w", err)
	}
	var cfg RulesConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing rules file: %w", err)
	}
	for i, r := range cfg.Rules {
		if r.Name == "" {
			return nil, fmt.Errorf("rule %d: name is required", i)
		}
		if r.Type != "no_dependency" && r.Type != "require_dependency" {
			return nil, fmt.Errorf("rule %q: type must be no_dependency or require_dependency", r.Name)
		}
	}
	return &cfg, nil
}

// CheckCustomRules evaluates custom rules against the graph.
func CheckCustomRules(graph *model.ArchGraph, rules *RulesConfig, rootPath string) []Violation {
	var violations []Violation
	for _, rule := range rules.Rules {
		switch rule.Type {
		case "no_dependency":
			violations = append(violations, checkNoDependency(graph, rule, rootPath)...)
		case "require_dependency":
			violations = append(violations, checkRequireDependency(graph, rule, rootPath)...)
		}
	}
	return violations
}

func checkNoDependency(graph *model.ArchGraph, rule Rule, rootPath string) []Violation {
	var violations []Violation
	for _, e := range graph.Edges() {
		sourceNode := graph.GetNode(e.Source)
		targetNode := graph.GetNode(e.Target)
		if sourceNode == nil || targetNode == nil {
			continue
		}
		if matchNode(sourceNode, rule.From, rootPath) && matchNode(targetNode, rule.To, rootPath) {
			violations = append(violations, Violation{
				Rule:     rule.Name,
				Severity: parseSeverity(rule.Severity),
				Subject:  fmt.Sprintf("%s -> %s", sourceNode.Name, targetNode.Name),
				Detail:   ruleDetail(rule, sourceNode, targetNode),
			})
		}
	}
	return violations
}

func checkRequireDependency(graph *model.ArchGraph, rule Rule, rootPath string) []Violation {
	sources := findMatchingNodes(graph, rule.From, rootPath)
	if len(sources) == 0 {
		return nil
	}

	var violations []Violation
	for _, src := range sources {
		if hasMatchingDependency(graph, src, rule.To, rootPath) {
			continue
		}
		violations = append(violations, Violation{
			Rule:     rule.Name,
			Severity: parseSeverity(rule.Severity),
			Subject:  src.Name,
			Detail:   fmt.Sprintf("%s: %s %q has no dependency matching target criteria", rule.Name, src.Type, src.Name),
		})
	}
	return violations
}

// findMatchingNodes returns every node that satisfies matcher.
func findMatchingNodes(graph *model.ArchGraph, matcher Matcher, rootPath string) []*model.Node {
	var out []*model.Node
	for _, n := range graph.Nodes() {
		if matchNode(n, matcher, rootPath) {
			out = append(out, n)
		}
	}
	return out
}

// hasMatchingDependency reports whether src has at least one outgoing edge
// pointing to a node that satisfies matcher.
func hasMatchingDependency(graph *model.ArchGraph, src *model.Node, matcher Matcher, rootPath string) bool {
	for _, e := range graph.EdgesFrom(src.ID) {
		target := graph.GetNode(e.Target)
		if target != nil && matchNode(target, matcher, rootPath) {
			return true
		}
	}
	return false
}

func matchNode(node *model.Node, matcher Matcher, rootPath string) bool {
	if matcher.Type == "" && matcher.Path == "" {
		return false
	}
	if matcher.Type != "" && string(node.Type) != matcher.Type {
		return false
	}
	if matcher.Path != "" && !pathMatches(matcher.Path, node.Path, rootPath) {
		return false
	}
	return true
}

// pathMatches checks whether nodePath, made relative to rootPath, matches
// pattern either directly or with the directory portion (which lets a
// directory glob like "internal/foo" match files inside it).
func pathMatches(pattern, nodePath, rootPath string) bool {
	rel := relativizePath(nodePath, rootPath)
	if matched, _ := filepath.Match(pattern, rel); matched {
		return true
	}
	matched, _ := filepath.Match(pattern, filepath.Dir(rel))
	return matched
}

// relativizePath strips rootPath (and the following separator) from p when
// p sits underneath it. Otherwise p is returned unchanged.
func relativizePath(p, rootPath string) string {
	if rootPath == "" || !strings.HasPrefix(p, rootPath) {
		return p
	}
	return strings.TrimPrefix(strings.TrimPrefix(p, rootPath), string(filepath.Separator))
}

func parseSeverity(s string) model.DiffSeverity {
	switch strings.ToLower(s) {
	case "critical":
		return model.SeverityCritical
	case "high":
		return model.SeverityHigh
	case "medium":
		return model.SeverityMedium
	case "low":
		return model.SeverityLow
	default:
		return model.SeverityMedium
	}
}

func ruleDetail(rule Rule, source, target *model.Node) string {
	if rule.Description != "" {
		return fmt.Sprintf("%s: %s (%s) -> %s (%s)", rule.Description, source.Name, source.Type, target.Name, target.Type)
	}
	return fmt.Sprintf("%s: %s (%s) -> %s (%s)", rule.Name, source.Name, source.Type, target.Name, target.Type)
}
