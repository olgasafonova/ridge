package python

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func writePyFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnalyze_BasicModule(t *testing.T) {
	dir := t.TempDir()
	path := writePyFile(t, dir, "app.py", `import os
import sys

def main():
    pass
`)

	a := New()
	nodes, edges, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if len(nodes) == 0 {
		t.Fatal("expected at least 1 node")
	}

	foundMod := false
	for _, n := range nodes {
		if n.Type == model.NodeModule && n.Name == "app" {
			foundMod = true
		}
	}
	if !foundMod {
		t.Fatal("expected to find module node 'app'")
	}

	foundDep := false
	for _, e := range edges {
		if e.Type == model.EdgeDependency && e.Label == "os" {
			foundDep = true
		}
	}
	if !foundDep {
		t.Fatal("expected dependency edge for os import")
	}
}

func TestAnalyze_FromImport(t *testing.T) {
	dir := t.TempDir()
	path := writePyFile(t, dir, "views.py", `from flask import Flask, render_template

app = Flask(__name__)
`)

	a := New()
	_, edges, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	foundDep := false
	for _, e := range edges {
		if e.Type == model.EdgeDependency && e.Label == "flask" {
			foundDep = true
		}
	}
	if !foundDep {
		t.Fatal("expected dependency edge for flask import")
	}
}

func TestAnalyze_DetectsDatabase(t *testing.T) {
	dir := t.TempDir()
	path := writePyFile(t, dir, "db.py", `from sqlalchemy import create_engine

engine = create_engine("postgresql://localhost/mydb")
`)

	a := New()
	nodes, edges, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	foundDB := false
	for _, n := range nodes {
		if n.Type == model.NodeDatabase {
			foundDB = true
		}
	}
	if !foundDB {
		t.Fatal("expected database node from sqlalchemy import")
	}

	foundRW := false
	for _, e := range edges {
		if e.Type == model.EdgeReadWrite {
			foundRW = true
		}
	}
	if !foundRW {
		t.Fatal("expected read_write edge to database")
	}
}

func TestAnalyze_DetectsQueue(t *testing.T) {
	dir := t.TempDir()
	path := writePyFile(t, dir, "tasks.py", `from celery import Celery

app = Celery('tasks')
`)

	a := New()
	nodes, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	foundQueue := false
	for _, n := range nodes {
		if n.Type == model.NodeQueue {
			foundQueue = true
		}
	}
	if !foundQueue {
		t.Fatal("expected queue node from celery import")
	}
}

func TestAnalyze_DetectsCache(t *testing.T) {
	dir := t.TempDir()
	path := writePyFile(t, dir, "cache.py", `import redis

r = redis.Redis()
`)

	a := New()
	nodes, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	foundCache := false
	for _, n := range nodes {
		if n.Type == model.NodeCache {
			foundCache = true
		}
	}
	if !foundCache {
		t.Fatal("expected cache node from redis import")
	}
}

func TestAnalyze_DetectsHTTPClient(t *testing.T) {
	dir := t.TempDir()
	path := writePyFile(t, dir, "client.py", `import requests

resp = requests.get("https://api.example.com")
`)

	a := New()
	nodes, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	foundAPI := false
	for _, n := range nodes {
		if n.Type == model.NodeExternalAPI {
			foundAPI = true
		}
	}
	if !foundAPI {
		t.Fatal("expected external API node from requests import")
	}
}

func TestAnalyze_FlaskRoute(t *testing.T) {
	dir := t.TempDir()
	path := writePyFile(t, dir, "routes.py", `from flask import Flask

app = Flask(__name__)

@app.route('/health')
def health():
    return {'status': 'ok'}
`)

	a := New()
	nodes, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	foundEndpoint := false
	for _, n := range nodes {
		if n.Type == model.NodeEndpoint {
			foundEndpoint = true
			if n.Properties["route"] != "/health" {
				t.Fatalf("expected route /health, got %s", n.Properties["route"])
			}
			if n.Properties["method"] != "route" {
				t.Fatalf("expected method route, got %s", n.Properties["method"])
			}
			if n.Properties["framework"] != "flask" {
				t.Fatalf("expected framework flask, got %s", n.Properties["framework"])
			}
		}
	}
	if !foundEndpoint {
		t.Fatal("expected endpoint node from @app.route('/health')")
	}
}

func TestAnalyze_FastAPIRoute(t *testing.T) {
	dir := t.TempDir()
	path := writePyFile(t, dir, "main.py", `from fastapi import FastAPI

app = FastAPI()

@app.get('/users')
async def get_users():
    return []
`)

	a := New()
	nodes, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	foundEndpoint := false
	for _, n := range nodes {
		if n.Type == model.NodeEndpoint {
			foundEndpoint = true
			if n.Properties["route"] != "/users" {
				t.Fatalf("expected route /users, got %s", n.Properties["route"])
			}
			if n.Properties["method"] != "get" {
				t.Fatalf("expected method get, got %s", n.Properties["method"])
			}
			if n.Properties["framework"] != "fastapi" {
				t.Fatalf("expected framework fastapi, got %s", n.Properties["framework"])
			}
		}
	}
	if !foundEndpoint {
		t.Fatal("expected endpoint node from @app.get('/users')")
	}
}

func TestAnalyze_MultipleRoutes(t *testing.T) {
	dir := t.TempDir()
	path := writePyFile(t, dir, "api.py", `from fastapi import FastAPI

app = FastAPI()

@app.get('/users')
async def list_users():
    return []

@app.post('/users')
async def create_user():
    return {}

@app.delete('/users/{id}')
async def delete_user(id: int):
    return {}
`)

	a := New()
	nodes, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	endpointCount := 0
	for _, n := range nodes {
		if n.Type == model.NodeEndpoint {
			endpointCount++
		}
	}
	if endpointCount != 3 {
		t.Fatalf("expected 3 endpoints, got %d", endpointCount)
	}
}

func TestAnalyze_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := writePyFile(t, dir, "bad.py", `}{}{not valid python at all {{{{`)

	a := New()
	// tree-sitter recovers gracefully from parse errors; should not crash
	_, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("tree-sitter should handle invalid input gracefully, got: %v", err)
	}
}

func TestExtensions(t *testing.T) {
	a := New()
	exts := a.Extensions()
	if len(exts) != 1 {
		t.Fatalf("expected 1 extension, got %d", len(exts))
	}
	if exts[0] != ".py" {
		t.Fatalf("expected .py, got %s", exts[0])
	}
}

func TestLanguage(t *testing.T) {
	a := New()
	if a.Language() != "python" {
		t.Fatalf("expected 'python', got %s", a.Language())
	}
}

// TestAnalyze_OversizedFileSkipsParsing verifies that files past maxFileBytes
// return a stub module node with skipped=size and no edges. Prevents the
// 10 MiB-of-nested-parens DoS reproducer documented in beads-he5.
func TestAnalyze_OversizedFileSkipsParsing(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, maxFileBytes+1024)
	for i := range big {
		big[i] = ' '
	}
	path := writePyFile(t, dir, "huge.py", string(big))

	nodes, edges, err := New().Analyze(path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 stub node, got %d", len(nodes))
	}
	if nodes[0].Type != model.NodeModule {
		t.Fatalf("want module node, got %s", nodes[0].Type)
	}
	if nodes[0].Properties["skipped"] != "size" {
		t.Fatalf("expected skipped=size, got %v", nodes[0].Properties)
	}
	if len(edges) != 0 {
		t.Fatalf("want 0 edges over cap, got %d", len(edges))
	}
}
