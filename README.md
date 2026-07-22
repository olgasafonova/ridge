# Ridge

[![Go Report Card](https://goreportcard.com/badge/github.com/olgasafonova/ridge)](https://goreportcard.com/report/github.com/olgasafonova/ridge)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

An MCP server that reverse-engineers architecture from a directory tree. Point it at code (Go, TypeScript, Python) and it returns services, packages, databases, queues, endpoints, and their relationships as a structured graph. Point it at markdown (Obsidian vaults, doc trees) and wiki-links plus relative `.md` links become dependency edges in the same graph model. Generate diagrams in 9 formats including a self-contained D3 force-directed page for hub-spoke graphs. Detect drift between any two branches, tags, or commits. Validate architecture rules. Track how the system evolves over time.

No configuration files, no manual diagramming. Static analysis builds the architecture model directly from the source tree.

## Why

Architecture diagrams go stale the day you commit them. When AI generates code faster than teams can comprehend the changes, the gap between system complexity and shared understanding grows. This is cognitive debt, and it compounds silently.

Most teams know they should review architecture regularly, check for circular dependencies, and catch structural drift between branches. In practice, these tasks are manual enough that they don't happen.

- Generates architecture from the source tree (code or markdown), so diagrams are always current. No one has to maintain them.
- `arch_validate` turns "check for circular dependencies" from a retro action item into a one-prompt task.
- `arch_drift` compares architecture between any two git refs. It catches structural changes that code review misses: a new database dependency, a service that quietly became a monolith, an endpoint that bypasses the API gateway.
- `arch_drift_explain` wraps that diff in a 2-5 sentence narrative you can paste straight into a PR description, standup channel, or release note. No LLM call; pure templating from the structured diff.

### The killer use case: onboarding onto code you don't own

The single highest-value moment for Ridge is the one every engineer hits weekly: **you have to change a codebase your team didn't build.** The old kickoff for that work was reading the source, tracing dependencies, and building a mental model in your head. When an agent writes the code, that step disappears, and with it the understanding. So agents change systems nobody on the calling team actually comprehends, and architecture decisions get made by whoever (or whatever) writes the first line.

Ridge replaces the missing step. Point it at the unfamiliar tree and `arch_scan` + `arch_generate` produce a current architecture model — services, packages, infra, endpoints, and their edges — that an engineer can read in minutes instead of reverse-engineering by hand. The owning team can react to *that*, rather than to a Slack thread or a finished PR, so alignment happens before code exists instead of after.

This isn't hypothetical. Miro demoed the identical pattern at Canvas 26 ("Give Your Agentic Coding Tools the Full Picture," June 2026): a feature team needed to add tools to an MCP server another team owned, used an agent to read that codebase and generate an architecture board, and used it to onboard themselves and align with the owning team — without waiting on a backlog. Same problem, same shape, independently arrived at. Ridge makes that the default workflow rather than a one-off prompt.

<!-- TODO: Add demo GIF showing arch_scan + arch_generate on a real project -->

## What it does

**Parses source files with language-specific analyzers** (Go via `go/ast`, TypeScript and Python via tree-sitter, markdown via regex-based link extraction) and builds an architecture graph of nodes and edges.

**Nodes** represent components: services, modules, packages, databases, message queues, caches, external APIs, HTTP endpoints, and notes (markdown files).

**Edges** represent relationships with confidence scores: dependencies (0.9), endpoint registrations (0.85), infrastructure links (0.8), HTTP client calls (0.7). Confidence lets consumers filter by reliability; direct AST-resolved imports score higher than heuristic matches.

[MCP](https://modelcontextprotocol.io/) (Model Context Protocol) lets AI assistants call external tools. This server gives your AI assistant 19 architecture analysis tools.

## What you get

Run `arch_scan` on a Go project and get back a structured architecture graph:

```json
{
  "topology": "monorepo",
  "nodes": [
    {"id": "pkg:api/server", "name": "server", "type": "package", "language": "go"},
    {"id": "pkg:worker/processor", "name": "processor", "type": "package", "language": "go"},
    {"id": "infra:postgresql", "name": "PostgreSQL", "type": "database"},
    {"id": "infra:redis", "name": "Redis", "type": "cache"},
    {"id": "infra:nats", "name": "NATS", "type": "queue"}
  ],
  "edges": [
    {"source": "pkg:api/server", "target": "infra:postgresql", "type": "read_write", "confidence": 0.8},
    {"source": "pkg:api/server", "target": "infra:redis", "type": "read_write", "confidence": 0.8},
    {"source": "pkg:worker/processor", "target": "infra:nats", "type": "subscribe", "confidence": 0.8}
  ],
  "stats": {"files_analyzed": 47, "files_cached": 38, "files_changed": 9, "nodes_found": 12, "edges_found": 23, "duration_ms": 340}
}
```

Then ask `arch_generate` for a Mermaid diagram, `arch_validate` to check for circular dependencies, or `arch_dataflow` for structured traces showing how requests flow from endpoints to databases. Infrastructure (databases, queues, caches) is detected automatically from import paths.

## Why not X

Four projects share parts of this space. None cover it the same way.

**[CodeFlow](https://github.com/braedonsaunders/codeflow)** (2.0k stars, MIT) is a single-file React + D3 page that parses JS, TypeScript, and Python in the browser. It reads from GitHub's API, renders a file-level dependency graph, and adds panels for blast radius, security scans, and PR impact. Browser-native, file-granular. No Go support; requires CDN access; no agent interface.

**[CodeGraphContext](https://github.com/CodeGraphContext/CodeGraphContext)** (1.2k stars, alpha) is a Python MCP server that indexes code into KuzuDB, FalkorDB, or Neo4j via tree-sitter. 14-language support, dual CLI/MCP mode. Same agent surface as ridge, without endpoint detection, infrastructure classification, drift narratives, or cross-substrate scanning (code-only, no markdown).

**[Graphify](https://github.com/safishamsi/graphify)** (35k stars, MIT) is a multi-harness skill that turns any folder into a queryable knowledge graph: 25 code languages plus markdown, images, and video transcription. Persistent `graph.json`, SHA256 incremental cache, `merge-graphs` for cross-repo composition. Skill-scope, not MCP. No drift detection, no architecture-specific node types, no infrastructure inference.

**[CocoIndex](https://github.com/cocoindex-io/cocoindex)** (9.7k stars, Apache-2.0) is a Python-front, Rust-core incremental indexing framework that turns codebases, meeting notes, inboxes, Slack, PDFs, and videos into vector-searchable chunks for RAG. Code is one substrate of many. Different problem class: cocoindex answers "find me code semantically similar to query Q"; ridge answers "show me the structural graph and what changed architecturally between two refs." Use cocoindex when you want semantic chunk search across heterogeneous substrates. Use ridge when you want a typed architecture model with drift detection.

**What ridge focuses on.** Agent-facing MCP transport across all 19 tools. Go-native via `go/ast`. Drift narratives: `arch_drift_explain` returns a paste-ready PR paragraph in one call, no LLM round-trip. Cross-substrate: code and markdown share the same graph model, so `arch_blast_radius` answers "if I change `internal/scanner`, what else needs review?" across both. Infrastructure-aware (databases, queues, caches as typed nodes with confidence scores).

**What ridge doesn't do.** No browser-native UI. No multimodal ingest (images, video, audio). No semantic chunk search for RAG. If you want a file-level visualizer for JS/TS/Python with a UI, use CodeFlow. If you want a multimodal knowledge graph at skill scope, use Graphify. If you want incremental vector indexing across heterogeneous corpora, use CocoIndex.

## Usage examples

Once configured, ask your LLM:

- "Scan the architecture of ~/Projects/my-app"
- "Generate a C4 diagram of this project"
- "Are there any circular dependencies or layering violations?"
- "Grandfather our existing violations, then fail only on new ones" (uses arch_validate baseline mode)
- "Do our tsconfig path aliases and go.mod replace targets actually exist?"
- "Compare architecture between the v1.0 tag and main branch"
- "How has the architecture changed since last month?"
- "What databases does this service connect to?"
- "Export the architecture as Excalidraw"
- "Explain the architecture decisions in this codebase"
- "How should I improve this architecture?"
- "What's the coupling and instability like?"
- "Show me data flow traces from API endpoints to databases"
- "Save this architecture as our v2.0 baseline"
- "Scan this monorepo but limit to 500 files and skip test files"
- "Scan an Obsidian vault and find orphan notes"
- "Render the docs/ directory as a force-directed graph showing hubs at degree>=10"
- "If I change internal/scanner what else needs review?" (uses arch_blast_radius)

## Tools

### Key tools

| Tool | What it does |
|------|-------------|
| `arch_scan` | Scan a codebase or markdown directory and return the full architecture graph with confidence-scored edges |
| `arch_generate` | Generate a diagram (Mermaid, PlantUML, C4, Structurizr, JSON, draw.io, Excalidraw, HTML, forcegraph) |
| `arch_blast_radius` | Find every node that transitively depends on a target — answers "if I change X, what else needs review?" |
| `arch_drift` | Compare architecture between two branches, tags, or commits |
| `arch_drift_explain` | Compare two refs and return a 2-5 sentence narrative summary plus the structured diff — paste-ready for PR descriptions |
| `arch_dataflow` | Trace data flow from endpoints to data stores with structured process traces |
| `arch_validate` | Check for circular dependencies, orphan nodes, layering violations, and environment inconsistencies — with a baseline mode that grandfathers existing violations and fails only on new ones |
| `arch_recommend` | Produce prioritized architecture improvement recommendations, each carrying the triggering metric evidence (value + threshold + node) and a confidence grade |

### All 19 tools

| Tool | Category | What it does |
|------|----------|-------------|
| `arch_scan` | analysis | Scan a codebase or markdown directory and return the full architecture graph |
| `arch_focus` | analysis | Scan a specific subdirectory or service |
| `arch_dependencies` | analysis | Map internal, external, and infrastructure dependencies |
| `arch_blast_radius` | analysis | Find the transitive set of nodes that depend on a target file or package |
| `arch_dataflow` | analysis | Trace data flow with entry-to-terminal process traces and confidence scores |
| `arch_boundaries` | analysis | Detect service boundaries and topology (monolith, monorepo, microservices), with the signals behind the verdict, the reason it fired, and an ambiguity flag for borderline calls |
| `arch_explain` | analysis | Explain topology, patterns, key decisions, and risks with code evidence |
| `arch_generate` | diagram | Generate a diagram in 9 formats |
| `arch_diff` | drift | Compare current architecture against a saved baseline |
| `arch_drift` | drift | Compare architecture between two git refs |
| `arch_drift_explain` | drift | Narrative summary of drift between two git refs (paste-ready prose) |
| `arch_validate` | validation | Check circular dependencies, orphans, layering, and environment inconsistencies; baseline mode ratchets legacy codebases |
| `arch_metrics` | validation | Compute coupling, instability, and dependency depth scores |
| `arch_recommend` | validation | Prioritized improvement recommendations from metrics + violations + patterns, with per-rec evidence + confidence |
| `arch_history` | history | Show how architecture evolved over git history |
| `arch_snapshot` | export | Save current architecture as a baseline for drift detection |
| `arch_registry_add` | registry | Register a repo by alias for reuse across tool calls |
| `arch_registry_list` | registry | List all registered repos |
| `arch_registry_remove` | registry | Remove a registered repo alias |

### Known-violations baseline (ratchet)

Adopting `arch_validate` on a legacy codebase usually means a wall of pre-existing violations and a permanently red check. The baseline mode fixes the incentive: grandfather what exists, fail only on what's new.

- `baseline="write"` saves the current violations to `.arch-known-violations.json` at the repo root. Commit the file so the whole team shares the ratchet.
- `baseline="check"` validates as usual, but `valid` reflects only violations absent from the baseline. New ones land in `new_violations`; the grandfathered count is reported alongside.

The full violation list stays in `violations` either way — a baseline changes the verdict, never hides findings. Matching keys on rule + subject, so a ridge upgrade that rewords violation details won't invalidate a committed baseline. Override the file location with `baseline_file` (contained to the scanned repo).

### Environment inconsistency checks

`arch_validate` also cross-checks declared module resolution against the filesystem, catching config drift that breaks builds without ever showing up in the import graph:

| Rule | Severity | What it catches |
|------|----------|----------------|
| `go_mod_replace_target_missing` | high | go.mod `replace` directive pointing at a local path that doesn't exist |
| `tsconfig_baseurl_missing` | high | tsconfig `baseUrl` directory missing — every path alias resolves against it |
| `tsconfig_path_target_missing` | medium | tsconfig `paths` alias mapping to a location that doesn't exist |

tsconfig parsing tolerates JSONC (comments and trailing commas), and the walk honors the scanner's skip list (`node_modules`, `vendor`, ...).

## Supported languages

| Language | Analyzer | Detection |
|----------|---------|-----------|
| Go | `go/ast` (stdlib) | Packages, imports, HTTP handlers, infrastructure |
| TypeScript/TSX | tree-sitter | Modules, imports, Express/Fastify/Koa routes, infrastructure |
| Python | tree-sitter | Modules, imports, Flask/FastAPI routes, infrastructure |
| Markdown | regex link extraction | Notes, Obsidian wiki-links `[[note]]`, relative `[text](./file.md)` links |

## Infrastructure detection

Analyzers recognize common infrastructure packages and classify them automatically:

| Category | Go | TypeScript | Python |
|----------|-----|-----------|--------|
| **Database** | database/sql, gorm, pgx, sqlx | pg, prisma, typeorm, mongoose, sequelize, drizzle-orm | sqlalchemy, django.db, pymongo, psycopg2, peewee, tortoise |
| **Queue** | amqp, kafka, nats | kafkajs, bullmq, amqplib, nats | celery, kombu, pika, kafka, rq |
| **Cache** | redis, memcache | ioredis, redis, keyv | redis, pymemcache, aiocache |
| **HTTP client** | net/http (client) | axios, node-fetch, got, undici | requests, httpx, aiohttp, urllib3 |

## Output formats

| Format | Description |
|--------|------------|
| Mermaid | Flowchart syntax, renders in GitHub, Notion, most markdown viewers |
| PlantUML | Component diagrams with UML notation |
| C4 | C4-PlantUML container diagrams with `!include <C4/C4_Container>` |
| Structurizr DSL | Workspace model for Structurizr tooling |
| JSON | Structured data with nodes, edges, topology metadata |
| draw.io | XML format, open directly in diagrams.net |
| Excalidraw | JSON format, open directly in Excalidraw |
| HTML | Self-contained page with the Mermaid runtime embedded inline (~900 KB output, no network requests) |
| forcegraph | Self-contained D3-driven force-directed page (~290 KB) with drag, zoom, pan; color = connected component; node size scales with degree. Use for hub-spoke graphs (knowledge vaults, dense dependency networks) where Mermaid's hierarchical layout produces a long horizontal stripe. Pair with `min_degree=10` to keep only hubs |

## Setup

### Prerequisites

- Go 1.24+
- C compiler (for tree-sitter CGo bindings; standard on macOS and Linux)

### Install

```bash
go install github.com/olgasafonova/ridge/cmd/ridge@latest
```

The binary lands in `$GOPATH/bin` (typically `~/go/bin/ridge`).

### Or build from source

```bash
git clone https://github.com/olgasafonova/ridge.git
cd ridge
make build
```

### Configure in Claude Code

Add to your `~/.claude.json`:

```json
{
  "mcpServers": {
    "ridge": {
      "command": "/path/to/ridge",
      "args": []
    }
  }
}
```

Or run from source:

```json
{
  "mcpServers": {
    "ridge": {
      "command": "go",
      "args": ["run", "./cmd/ridge"],
      "cwd": "/path/to/ridge"
    }
  }
}
```

### Optional: install the bundled Claude Code skill

`skills/ridge/SKILL.md` teaches an agent which of the 19 ridge tools to call for a given question (blast radius vs scan vs drift vs validate). Drop it into your Claude Code skills directory once and the model stops guessing.

```bash
cp -r skills/ridge ~/.claude/skills/
```

The skill is plain markdown with a tool-decision matrix and worked examples. Read it directly if you want the same map in your head.

## Scan control

All scan tools accept optional parameters for handling large codebases:

| Parameter | What it does |
|-----------|-------------|
| `max_files` | Stop after analyzing N files (returns partial result) |
| `max_nodes` | Stop after discovering N architecture nodes |
| `timeout_secs` | Cancel scan after N seconds |
| `workers` | Parallel analysis workers (default: CPU count, max 8) |
| `skip_dirs` | Additional directories to skip (beyond defaults like `node_modules`, `.git`, `vendor`) |
| `skip_globs` | File patterns to skip (e.g. `*_test.go`, `*.spec.ts`) |

Partial results include a `truncated: true` flag so you know the graph is incomplete. Sequential tool calls on the same path are cached for 30 seconds.

## Incremental scanning

Repeat scans on the same codebase are fast. The server tracks file modification times and content hashes in `~/.mcp-context/ridge/`. On subsequent scans, only files that actually changed get re-analyzed; unchanged files reuse cached analysis results.

The stats in the response show what happened:
- `files_analyzed` — total files in the codebase
- `files_cached` — files skipped (unchanged since last scan)
- `files_changed` — files re-analyzed (new, modified, or deleted)

First scan of a 500-file project takes a few seconds. Follow-up scans after editing 3 files take milliseconds.

## Security

### Restricting scan roots (`RIDGE_ALLOWED_DIRS`)

Every scan tool returns short, masked source samples from whatever directory it is pointed at. By default ridge will scan any readable directory except a built-in denylist of sensitive locations (`/etc`, `/proc`, `/sys`, `/dev`, and home dotfiles like `.ssh`, `.gnupg`, `.aws`, `.config/gcloud`). That denylist is a floor, not an allowlist: it blocks known-sensitive paths but still permits scanning anywhere else.

To flip this to opt-in, set `RIDGE_ALLOWED_DIRS` to a colon-separated list of absolute directory paths (PATH-style). When set, ridge refuses any scan whose target resolves to a location outside those directories:

```json
{
  "mcpServers": {
    "ridge": {
      "command": "/path/to/ridge",
      "args": [],
      "env": {
        "RIDGE_ALLOWED_DIRS": "/home/you/Projects:/home/you/work/repos"
      }
    }
  }
}
```

Details:

- **Symlink-resolved containment.** Both the scan target and the allowlisted directories are resolved with `filepath.EvalSymlinks` before the containment check, so a symlink placed at or under an allowlisted directory cannot redirect a scan to an outside target.
- **Fail closed when misconfigured.** If `RIDGE_ALLOWED_DIRS` is set but none of its entries resolve to a real directory, every scan is refused rather than reverting to permit-all.
- **Startup notice.** On launch, ridge logs whether the allowlist is active (with the resolved directories) or unset. An unset allowlist logs a one-line warning so the permissive default is visible in the server logs.

The `RIDGE_ALLOW_INREPO_RULES` variable is separate: it opts into loading a repo's own `.arch-rules.yaml`, which is otherwise ignored to prevent a scanned repo from downgrading its own architecture-rule violations.

## Development

```bash
make check       # fmt-check + vet + tests (with race detector)
make build       # Build binary
make test        # Tests only
```

### Integration tests

Run against real codebases:

```bash
go test -tags integration -race -v ./tests/
```

Or use the smoke test script:

```bash
bash scripts/smoke-test.sh
```

## Architecture

```
cmd/ridge/          Entry point (stdio MCP transport)
internal/
  model/                   ArchGraph, Node, Edge, Diff types
  scanner/                 File walker, incremental change detection, analyzer orchestration
  analyzer/golang/         Go static analysis (go/ast)
  analyzer/typescript/     TypeScript analysis (tree-sitter)
  analyzer/python/         Python analysis (tree-sitter)
  analyzer/markdown/       Markdown link extraction (wiki-links, relative .md links)
  detector/                Boundary detection, topology, validation, metrics, recommendations, process traces
  drift/                   Snapshot comparison, git ref diffing, history
  render/                  Mermaid, PlantUML, C4, Structurizr, JSON, draw.io, Excalidraw, HTML, forcegraph
  infra/                   Cache, persistent state (~/.mcp-context/)
tools/                     MCP tool definitions and handlers
```

## License

MIT
