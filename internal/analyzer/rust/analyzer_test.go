package rust

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func writeRust(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestAnalyze_ExtractsModuleAndImports(t *testing.T) {
	src := `use sqlx::PgPool;
use reqwest::Client;
use std::collections::HashMap;

fn main() {}
`
	path := writeRust(t, "main.rs", src)
	a := New()

	nodes, edges, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if !hasNodeOfType(nodes, model.NodeModule) {
		t.Fatalf("expected a module node, got nodes=%v", nodeIDs(nodes))
	}
	if !hasEdgeWithLabel(edges, "sqlx") {
		t.Fatalf("expected dependency edge with label 'sqlx', got %v", edgeLabels(edges))
	}
	if !hasNodeOfType(nodes, model.NodeDatabase) {
		t.Fatalf("expected infra:database node from sqlx import")
	}
	if !hasNodeOfType(nodes, model.NodeExternalAPI) {
		t.Fatalf("expected infra:external_api node from reqwest import")
	}
}

func TestAnalyze_DetectsActixRoute(t *testing.T) {
	src := `use actix_web::{get, web, App, HttpServer, Responder};

#[get("/users/{id}")]
async fn users(path: web::Path<u32>) -> impl Responder {
    "ok"
}
`
	path := writeRust(t, "lib.rs", src)
	a := New()

	nodes, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	ep := findNodeOfType(nodes, model.NodeEndpoint)
	if ep == nil {
		t.Fatalf("expected an endpoint node; nodes=%v", nodeIDs(nodes))
	}
	if got := ep.Properties["method"]; got != "GET" {
		t.Fatalf("endpoint method: got %q, want GET", got)
	}
	if got := ep.Properties["route"]; got != "/users/{id}" {
		t.Fatalf("endpoint route: got %q, want /users/{id}", got)
	}
	if got := ep.Properties["framework"]; got != "actix-web" {
		t.Fatalf("framework detected: got %q, want actix-web", got)
	}
}

func TestAnalyze_DetectsHTTPClientCall(t *testing.T) {
	src := `use reqwest;

async fn fetch() {
    let _ = reqwest::get("https://api.example.com/v1/users").await;
}
`
	path := writeRust(t, "client.rs", src)
	a := New()

	nodes, edges, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if findNodeByID(nodes, "service:api.example.com") == nil {
		t.Fatalf("expected service:api.example.com node; nodes=%v", nodeIDs(nodes))
	}
	if !hasEdgeWithLabelPrefix(edges, "GET https://api.example.com") {
		t.Fatalf("expected GET https://api.example.com edge; edges=%v", edgeLabels(edges))
	}
}

func TestAnalyze_SkipsOversizedFile(t *testing.T) {
	// Build a payload > maxFileBytes (5 MiB).
	big := make([]byte, maxFileBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	path := writeRust(t, "huge.rs", string(big))
	a := New()

	nodes, edges, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Properties["skipped"] != "size" {
		t.Fatalf("expected single skipped-size module, got %v", nodes)
	}
	if len(edges) != 0 {
		t.Fatalf("oversized file should emit no edges, got %d", len(edges))
	}
}

func TestExtractUsePath_GroupedImport(t *testing.T) {
	src := `use foo::bar::{Baz, Qux};

fn main() {}
`
	path := writeRust(t, "g.rs", src)
	a := New()

	_, edges, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !hasEdgeWithLabel(edges, "foo::bar") {
		t.Fatalf("grouped import should yield label 'foo::bar', got %v", edgeLabels(edges))
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
