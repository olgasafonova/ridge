package java

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func writeJava(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestAnalyze_ImportsAndInfra(t *testing.T) {
	src := `package com.example;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;
import javax.persistence.EntityManager;
import redis.clients.jedis.Jedis;

@RestController
public class UserController {
}
`
	path := writeJava(t, "UserController.java", src)
	a := New()

	nodes, edges, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if !hasEdgeWithLabel(edges, "org.springframework.web.bind.annotation") {
		t.Fatalf("expected dependency edge with stripped package, got %v", edgeLabels(edges))
	}
	if !hasNodeOfType(nodes, model.NodeDatabase) {
		t.Fatalf("expected infra:database from javax.persistence")
	}
	if !hasNodeOfType(nodes, model.NodeCache) {
		t.Fatalf("expected infra:cache from redis.clients")
	}
}

func TestAnalyze_DetectsSpringEndpoint(t *testing.T) {
	src := `package com.example;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class UserController {
    @GetMapping("/users/{id}")
    public String getUser(String id) {
        return id;
    }
}
`
	path := writeJava(t, "UserController.java", src)
	a := New()

	nodes, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	ep := findNodeOfType(nodes, model.NodeEndpoint)
	if ep == nil {
		t.Fatalf("expected endpoint node; got %v", nodeIDs(nodes))
	}
	if got := ep.Properties["method"]; got != "GET" {
		t.Fatalf("endpoint method: got %q, want GET", got)
	}
	if got := ep.Properties["route"]; got != "/users/{id}" {
		t.Fatalf("endpoint route: got %q, want /users/{id}", got)
	}
	if got := ep.Properties["framework"]; got != "spring" {
		t.Fatalf("framework: got %q, want spring", got)
	}
}

func TestAnalyze_DetectsRequestMappingMethod(t *testing.T) {
	src := `package com.example;

import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestMethod;

public class Controller {
    @RequestMapping(value = "/items", method = RequestMethod.POST)
    public String create() { return ""; }
}
`
	path := writeJava(t, "Controller.java", src)
	a := New()

	nodes, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	ep := findNodeOfType(nodes, model.NodeEndpoint)
	if ep == nil {
		t.Fatalf("expected endpoint node")
	}
	if got := ep.Properties["method"]; got != "POST" {
		t.Fatalf("RequestMapping method: got %q, want POST", got)
	}
}

func TestAnalyze_DetectsHTTPClientCall(t *testing.T) {
	src := `package com.example;

import org.springframework.web.client.RestTemplate;

public class ApiClient {
    private RestTemplate restTemplate = new RestTemplate();

    public String fetch() {
        return restTemplate.getForObject("https://api.example.com/v1/users", String.class);
    }
}
`
	path := writeJava(t, "ApiClient.java", src)
	a := New()

	nodes, edges, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if findNodeByID(nodes, "service:api.example.com") == nil {
		t.Fatalf("expected service:api.example.com; got %v", nodeIDs(nodes))
	}
	if !hasEdgeWithLabelPrefix(edges, "GET https://api.example.com") {
		t.Fatalf("expected GET https://api.example.com edge; got %v", edgeLabels(edges))
	}
}

func TestAnalyze_SkipsOversizedFile(t *testing.T) {
	big := make([]byte, maxFileBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	path := writeJava(t, "Huge.java", string(big))
	a := New()

	nodes, edges, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Properties["skipped"] != "size" {
		t.Fatalf("expected single skipped-size module, got %v", nodes)
	}
	if len(edges) != 0 {
		t.Fatalf("oversized file should emit no edges")
	}
}

// --- test helpers ---

func hasNodeOfType(nodes []*model.Node, t model.NodeType) bool {
	for _, n := range nodes {
		if n.Type == t {
			return true
		}
	}
	return false
}

func findNodeOfType(nodes []*model.Node, t model.NodeType) *model.Node {
	for _, n := range nodes {
		if n.Type == t {
			return n
		}
	}
	return nil
}

func findNodeByID(nodes []*model.Node, id string) *model.Node {
	for _, n := range nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func hasEdgeWithLabel(edges []*model.Edge, label string) bool {
	for _, e := range edges {
		if e.Label == label {
			return true
		}
	}
	return false
}

func hasEdgeWithLabelPrefix(edges []*model.Edge, prefix string) bool {
	for _, e := range edges {
		if len(e.Label) >= len(prefix) && e.Label[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func nodeIDs(nodes []*model.Node) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}

func edgeLabels(edges []*model.Edge) []string {
	labels := make([]string, len(edges))
	for i, e := range edges {
		labels[i] = e.Label
	}
	return labels
}
