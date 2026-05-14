package typescript

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olgasafonova/ridge/internal/model"
)

func writeTSFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnalyze_BasicModule(t *testing.T) {
	dir := t.TempDir()
	path := writeTSFile(t, dir, "app.ts", `import { Router } from 'express';

const router = Router();
export default router;
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
		if e.Type == model.EdgeDependency && e.Label == "express" {
			foundDep = true
		}
	}
	if !foundDep {
		t.Fatal("expected dependency edge for express import")
	}
}

func TestAnalyze_DetectsDatabase(t *testing.T) {
	dir := t.TempDir()
	path := writeTSFile(t, dir, "db.ts", `import { Pool } from 'pg';

const pool = new Pool();
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
		t.Fatal("expected database node from pg import")
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

func TestAnalyze_DetectsMessageQueue(t *testing.T) {
	dir := t.TempDir()
	path := writeTSFile(t, dir, "events.ts", `import { Kafka } from 'kafkajs';

const kafka = new Kafka({ brokers: ['localhost:9092'] });
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
		t.Fatal("expected queue node from kafkajs import")
	}
}

func TestAnalyze_DetectsCache(t *testing.T) {
	dir := t.TempDir()
	path := writeTSFile(t, dir, "cache.ts", `import Redis from 'ioredis';

const redis = new Redis();
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
		t.Fatal("expected cache node from ioredis import")
	}
}

func TestAnalyze_DetectsHTTPClient(t *testing.T) {
	dir := t.TempDir()
	path := writeTSFile(t, dir, "client.ts", `import axios from 'axios';

const res = await axios.get('/api');
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
		t.Fatal("expected external API node from axios import")
	}
}

func TestAnalyze_DetectsExpressEndpoint(t *testing.T) {
	dir := t.TempDir()
	path := writeTSFile(t, dir, "routes.ts", `import express from 'express';

const app = express();
app.get('/health', (req, res) => {
  res.json({ status: 'ok' });
});
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
			if n.Properties["method"] != "get" {
				t.Fatalf("expected method get, got %s", n.Properties["method"])
			}
		}
	}
	if !foundEndpoint {
		t.Fatal("expected endpoint node from app.get('/health', ...)")
	}
}

func TestAnalyze_MultipleRoutes(t *testing.T) {
	dir := t.TempDir()
	path := writeTSFile(t, dir, "api.ts", `import express from 'express';

const app = express();
app.get('/users', getUsers);
app.post('/users', createUser);
app.delete('/users/:id', deleteUser);
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

func TestAnalyze_TSXFile(t *testing.T) {
	dir := t.TempDir()
	path := writeTSFile(t, dir, "component.tsx", `import React from 'react';

const App: React.FC = () => {
  return <div>Hello</div>;
};

export default App;
`)

	a := New()
	nodes, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("TSX parsing failed: %v", err)
	}

	foundMod := false
	for _, n := range nodes {
		if n.Type == model.NodeModule && n.Name == "component" {
			foundMod = true
		}
	}
	if !foundMod {
		t.Fatal("expected module node for TSX file")
	}
}

func TestAnalyze_NestJSController(t *testing.T) {
	dir := t.TempDir()
	path := writeTSFile(t, dir, "users.controller.ts", `import { Controller, Get, Post } from '@nestjs/common';

@Controller('/users')
export class UsersController {
  @Get()
  findAll() {
    return [];
  }

  @Post()
  create() {
    return {};
  }
}
`)

	a := New()
	nodes, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	endpointCount := 0
	methods := map[string]bool{}
	for _, n := range nodes {
		if n.Type == model.NodeEndpoint {
			endpointCount++
			methods[n.Properties["method"]] = true
			if n.Properties["framework"] != "nestjs" {
				t.Fatalf("expected framework nestjs, got %s", n.Properties["framework"])
			}
		}
	}
	if endpointCount != 2 {
		t.Fatalf("expected 2 endpoints, got %d", endpointCount)
	}
	if !methods["get"] || !methods["post"] {
		t.Fatalf("expected get and post methods, got %v", methods)
	}
}

func TestAnalyze_NestJSControllerPath(t *testing.T) {
	dir := t.TempDir()
	path := writeTSFile(t, dir, "items.controller.ts", `import { Controller, Get, Post, Delete } from '@nestjs/common';

@Controller('/items')
export class ItemsController {
  @Get(':id')
  findOne() {}

  @Post('batch')
  createBatch() {}

  @Delete(':id')
  remove() {}
}
`)

	a := New()
	nodes, _, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	routes := map[string]string{}
	for _, n := range nodes {
		if n.Type == model.NodeEndpoint {
			routes[n.Properties["method"]] = n.Properties["route"]
		}
	}

	expected := map[string]string{
		"get":    "/items/:id",
		"post":   "/items/batch",
		"delete": "/items/:id",
	}

	for method, wantRoute := range expected {
		gotRoute, ok := routes[method]
		if !ok {
			t.Fatalf("missing %s endpoint", method)
		}
		if gotRoute != wantRoute {
			t.Fatalf("expected %s route %q, got %q", method, wantRoute, gotRoute)
		}
	}
}

func TestAnalyze_NestJSInjectable(t *testing.T) {
	dir := t.TempDir()
	path := writeTSFile(t, dir, "users.service.ts", `import { Injectable } from '@nestjs/common';

@Injectable()
export class UsersService {
  findAll() {
    return [];
  }
}
`)

	a := New()
	nodes, edges, err := a.Analyze(path)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	foundInjectable := false
	for _, n := range nodes {
		if n.Properties != nil && n.Properties["injectable"] == "true" {
			foundInjectable = true
			if n.Name != "UsersService" {
				t.Fatalf("expected injectable name UsersService, got %s", n.Name)
			}
		}
	}
	if !foundInjectable {
		t.Fatal("expected to find @Injectable service node")
	}

	foundProvides := false
	for _, e := range edges {
		if e.Label == "provides" {
			foundProvides = true
		}
	}
	if !foundProvides {
		t.Fatal("expected 'provides' edge for injectable service")
	}
}

func TestAnalyze_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := writeTSFile(t, dir, "bad.ts", `}{}{not valid anything at all {{{{`)

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
	if len(exts) != 2 {
		t.Fatalf("expected 2 extensions, got %d", len(exts))
	}
	expected := map[string]bool{".ts": true, ".tsx": true}
	for _, ext := range exts {
		if !expected[ext] {
			t.Fatalf("unexpected extension: %s", ext)
		}
	}
}

func TestLanguage(t *testing.T) {
	a := New()
	if a.Language() != "typescript" {
		t.Fatalf("expected 'typescript', got %s", a.Language())
	}
}

// TestAnalyze_OversizedFileSkipsParsing verifies that files past maxFileBytes
// return a stub module node with skipped=size and no edges. Defends against
// the same DoS surface as the Python analyzer; see beads-he5.
func TestAnalyze_OversizedFileSkipsParsing(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, maxFileBytes+1024)
	for i := range big {
		big[i] = ' '
	}
	path := writeTSFile(t, dir, "huge.ts", string(big))

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
