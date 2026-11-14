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

// writeJavaAt writes a Java file at a path below root, creating folders.
func writeJavaAt(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

const sameNameController = `package com.acme.web;

import org.springframework.web.bind.annotation.GetMapping;

public class Controller {
    @GetMapping("/ping")
    public String ping() { return ""; }
}
`

// Two modules of one build each have web/Controller.java. The parent-folder
// ID gave both "mod:web/Controller" and both endpoints "endpoint:Controller.java:6".
func TestAnalyzeRooted_SameNamedFilesGetOwnIDs(t *testing.T) {
	root := t.TempDir()
	billing := writeJavaAt(t, root, "billing/src/main/java/com/acme/web/Controller.java", sameNameController)
	orders := writeJavaAt(t, root, "orders/src/main/java/com/acme/web/Controller.java", sameNameController)
	a := New()

	bNodes, bEdges, err := a.AnalyzeRooted(root, billing)
	if err != nil {
		t.Fatalf("AnalyzeRooted billing: %v", err)
	}
	oNodes, _, err := a.AnalyzeRooted(root, orders)
	if err != nil {
		t.Fatalf("AnalyzeRooted orders: %v", err)
	}

	const bMod = "mod:billing/src/main/java/com/acme/web/Controller"
	const oMod = "mod:orders/src/main/java/com/acme/web/Controller"
	if bNodes[0].ID != bMod || oNodes[0].ID != oMod {
		t.Fatalf("module IDs: got %q and %q, want %q and %q", bNodes[0].ID, oNodes[0].ID, bMod, oMod)
	}
	if bNodes[0].Name != "Controller" {
		t.Fatalf("module name: got %q, want Controller", bNodes[0].Name)
	}

	const bEp = "endpoint:billing/src/main/java/com/acme/web/Controller.java:6"
	const oEp = "endpoint:orders/src/main/java/com/acme/web/Controller.java:6"
	if findNodeByID(bNodes, bEp) == nil || findNodeByID(oNodes, oEp) == nil {
		t.Fatalf("endpoint IDs: got %v and %v", nodeIDs(bNodes), nodeIDs(oNodes))
	}
	for _, e := range bEdges {
		if e.Source != bMod {
			t.Fatalf("edge source %q, want %q", e.Source, bMod)
		}
	}
}

func TestAnalyzeRooted_FallsBackOutsideRoot(t *testing.T) {
	path := writeJavaAt(t, t.TempDir(), "web/Controller.java", sameNameController)
	a := New()

	for _, root := range []string{"", t.TempDir()} {
		nodes, _, err := a.AnalyzeRooted(root, path)
		if err != nil {
			t.Fatalf("AnalyzeRooted: %v", err)
		}
		if nodes[0].ID != "mod:web/Controller" {
			t.Fatalf("root %q: module ID %q, want mod:web/Controller", root, nodes[0].ID)
		}
		if findNodeByID(nodes, "endpoint:Controller.java:6") == nil {
			t.Fatalf("root %q: endpoint IDs %v", root, nodeIDs(nodes))
		}
	}
}

func TestAnalyzeRooted_OversizedFileKeepsRootedID(t *testing.T) {
	root := t.TempDir()
	path := writeJavaAt(t, root, "gen/Huge.java", string(make([]byte, maxFileBytes+1)))

	nodes, _, err := New().AnalyzeRooted(root, path)
	if err != nil {
		t.Fatalf("AnalyzeRooted: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != "mod:gen/Huge" || nodes[0].Properties["skipped"] != "size" {
		t.Fatalf("got %v", nodes)
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
