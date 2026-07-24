// Code to Arch MCP - A Model Context Protocol server for codebase architecture analysis.
// Scans codebases, generates architecture diagrams, and detects architectural drift.
package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olgasafonova/ridge/internal/safepath"
	"github.com/olgasafonova/ridge/tools"
	"github.com/olgasafonova/ridge/tracing"
)

const (
	ServerName    = "ridge"
	ServerVersion = "0.1.0"
)

func main() {
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	announceAllowlistPosture(logger)

	shutdownTracing := setupTracing(logger)
	defer shutdownTracing()

	server := newServer(logger)
	registry := tools.NewHandlerRegistry(logger)
	registry.RegisterAll(server)

	run(server, logger)
}

// announceAllowlistPosture logs the scan-root allowlist posture once at
// startup. Unset means scans are permitted on any readable directory (minus
// the sensitive-path denylist); operators who want to restrict scan roots set
// RIDGE_ALLOWED_DIRS.
func announceAllowlistPosture(logger *slog.Logger) {
	if dirs, ok := safepath.AllowedDirs(); ok {
		logger.Info("scan-root allowlist active", "env", safepath.AllowedDirsEnv, "dirs", dirs)
	} else {
		logger.Warn("scan-root allowlist unset: scans permitted on any readable directory except the sensitive-path denylist; set RIDGE_ALLOWED_DIRS (colon-separated absolute paths) to restrict", "env", safepath.AllowedDirsEnv)
	}
}

// setupTracing initializes OpenTelemetry tracing and returns the shutdown
// function the caller must defer. Failures are non-fatal: the server runs
// without tracing and the returned shutdown is a no-op.
func setupTracing(logger *slog.Logger) func() {
	tracingConfig := tracing.DefaultConfig()
	tracingConfig.ServiceVersion = ServerVersion
	shutdown, err := tracing.Setup(context.Background(), tracingConfig)
	if err != nil {
		logger.Warn("Failed to initialize tracing", "error", err)
		return func() {}
	}
	if !tracingConfig.Enabled {
		return func() {}
	}
	logger.Info("OpenTelemetry tracing enabled",
		"endpoint", tracingConfig.OTLPEndpoint,
		"service", tracingConfig.ServiceName)
	return func() { _ = shutdown(context.Background()) }
}

// newServer creates the MCP server with capabilities and instructions.
func newServer(logger *slog.Logger) *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{
		Name:    ServerName,
		Version: ServerVersion,
	}, &mcp.ServerOptions{
		Logger: logger,
		// Suppress pre-initialize notifications/tools/list_changed from go-sdk.
		// Without this, AddTool triggers a notification before the client completes
		// the initialize handshake, causing intermittent connection failures.
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
		Instructions: serverInstructions,
	})
}

// run serves stdio traffic until a SIGINT/SIGTERM arrives or the transport
// closes.
func run(server *mcp.Server, logger *slog.Logger) {
	logger.Info("Starting Code to Arch MCP (stdio mode)",
		"name", ServerName,
		"version", ServerVersion,
	)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sig := <-sigChan
		logger.Info("Shutdown signal received", "signal", sig.String())
		cancel()
	}()

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && err != context.Canceled {
		log.Fatalf("Server error: %v", err)
	}
	logger.Info("Shutdown complete")
}

const serverInstructions = `Code to Arch MCP - Codebase Architecture Analysis

## Getting Started

Point any tool at a codebase directory path, or register repos by alias for repeated use.

### Register a repo (optional):
"Register this project for future scans"
-> USE: arch_registry_add (saves alias for path reuse)

### List registered repos:
"What repos are registered?"
-> USE: arch_registry_list

### Remove a repo:
"Remove the code-to-arch alias"
-> USE: arch_registry_remove

Once registered, use repo="alias" instead of path in any tool.

## Tool Selection Guide

### Full scan:
"Analyze the architecture of this project"
-> USE: arch_scan (returns nodes, edges, topology)

### Focused view:
"Show me just the API service"
-> USE: arch_focus (scans a subdirectory)

### Generate diagram:
"Create a Mermaid diagram of this project"
-> USE: arch_generate (outputs Mermaid, PlantUML, C4, Structurizr, draw.io, Excalidraw, JSON)

### Dependencies:
"What does this service depend on?"
-> USE: arch_dependencies (internal, external, infrastructure)

### Data flow:
"How does data flow through the system?"
-> USE: arch_dataflow (endpoints, data paths, stores)

### Boundaries:
"Is this a monolith or microservices?"
-> USE: arch_boundaries (topology detection, service boundaries)

### Drift from baseline:
"Has the architecture changed since our last review?"
-> USE: arch_diff (compare against saved snapshot)

### Drift between refs:
"What changed architecturally since v1.0?"
-> USE: arch_drift (compare two git refs)

### Validation:
"Are there any architecture problems?"
-> USE: arch_validate (circular deps, layering violations, custom .arch-rules.yaml)

### Fitness metrics:
"How healthy is the architecture?"
-> USE: arch_metrics (coupling, instability, dependency depth scores)

### History:
"How has the architecture evolved?"
-> USE: arch_history (evolution over git history)

### Save baseline:
"Save this as our v2.0 architecture"
-> USE: arch_snapshot (saves JSON for drift detection)

### Explanation:
"Why is it structured this way?"
-> USE: arch_explain (architecture rationale with code evidence)

## Supported Languages

Go (go/ast), TypeScript (tree-sitter), Python (tree-sitter)

## Output Formats

mermaid, plantuml, c4, structurizr, drawio, excalidraw, json`
