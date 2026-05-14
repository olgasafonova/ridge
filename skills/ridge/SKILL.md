---
name: ridge
description: Architecture analysis for codebases. Use when the user asks about repository structure, dependencies, blast radius, data flow, service boundaries, architecture drift, diagrams, or validation against rules. Triggers include "analyze architecture", "show me dependencies", "what would break if I change X", "draw a diagram of this project", "is this a monolith or microservices", "what changed architecturally since vN", "give me a Mermaid/PlantUML/C4/Structurizr/draw.io/Excalidraw diagram", "save this architecture as a baseline", "what's drifted since baseline". Do NOT use for single-function complexity (use code-review tools), runtime profiling (out of scope), or content questions about non-code files outside markdown vaults.
allowed-tools:
  - mcp__ridge__arch_scan
  - mcp__ridge__arch_focus
  - mcp__ridge__arch_dependencies
  - mcp__ridge__arch_blast_radius
  - mcp__ridge__arch_dataflow
  - mcp__ridge__arch_boundaries
  - mcp__ridge__arch_explain
  - mcp__ridge__arch_generate
  - mcp__ridge__arch_diff
  - mcp__ridge__arch_drift
  - mcp__ridge__arch_drift_explain
  - mcp__ridge__arch_validate
  - mcp__ridge__arch_metrics
  - mcp__ridge__arch_recommend
  - mcp__ridge__arch_history
  - mcp__ridge__arch_snapshot
  - mcp__ridge__arch_registry_add
  - mcp__ridge__arch_registry_list
  - mcp__ridge__arch_registry_remove
---

# ridge — Codebase Architecture Analysis

Ridge is an MCP server that scans codebases (Go, TypeScript, Python, Markdown), builds an architecture graph (nodes + edges), and answers structural questions about the result. This skill teaches when to call which of the 19 tools so you don't pick `arch_scan` for a question that `arch_blast_radius` answers better.

## How to read this skill

When the user asks an architecture question, find the matching row in **Tool selection** below, call that tool, and present the answer. Most questions need one tool. A few ("show me a diagram of the current state and what changed since last week") need two.

For repeat scans of the same repo, register it first with `arch_registry_add` (gives an alias) so subsequent calls pass `repo: "alias"` instead of an absolute path.

## Tool selection

| User intent | Tool | Notes |
|---|---|---|
| Full overview of a repo | `arch_scan` | Returns nodes, edges, detected topology. Start here when the user hasn't been specific. |
| Drill into one subdirectory | `arch_focus` | Same shape as scan, scoped to one path. Use when "just the API service" or "only show internal/handlers". |
| Internal vs external vs infrastructure deps | `arch_dependencies` | Returns three categorized lists. Use for "what does this depend on", "what external services does it call". |
| What gets affected if X changes | `arch_blast_radius` | Pass the node ID; returns downstream-impacted nodes. Use for "what breaks if I refactor module Y", "who calls function Z". |
| Endpoints, data paths, stores | `arch_dataflow` | Traces request → handler → DB. Use for "how does data flow", "what writes to the user table". |
| Monolith vs microservices, service boundaries | `arch_boundaries` | Topology detection. Use for "is this one service or many", "where are the seams". |
| Why it's structured this way | `arch_explain` | Narrative explanation using detected patterns. Use when the user wants reasoning, not raw graph data. |
| Generate a diagram | `arch_generate` | 9 formats: Mermaid, PlantUML, C4, Structurizr DSL, JSON, draw.io XML, Excalidraw JSON, self-contained HTML, D3 force-directed. Default to Mermaid unless the user names a format. |
| Compare against a saved baseline | `arch_diff` | Reads a snapshot JSON, compares to current scan. Use after `arch_snapshot`. |
| Compare two git refs | `arch_drift` | Scans both refs, returns the architectural delta. Use for "what changed since v1.0", "what did this PR change architecturally". |
| Narrative explanation of drift | `arch_drift_explain` | Same input as `arch_drift` but returns prose, not a delta list. Use when the user wants "explain why this drifted". |
| Validate against architecture rules | `arch_validate` | Detects circular deps, layering violations. Honors `.arch-rules.yaml` from a trusted location (env-gated). Use for "any architecture problems", "is this clean". |
| Architecture health metrics | `arch_metrics` | Coupling, instability, dependency depth. Use for "how healthy is this", "score this codebase". |
| Recommendations | `arch_recommend` | Suggests architectural improvements based on detected smells. Use for "what should we fix first". |
| Evolution over git history | `arch_history` | Walks commits, shows architecture over time. Use for "how has this evolved", "when did the cycle appear". |
| Save a baseline | `arch_snapshot` | Persists the current scan as JSON for later `arch_diff`. Use before a risky refactor. |
| Register a repo alias | `arch_registry_add` | Persists path + alias; subsequent tool calls pass `repo: "alias"`. Use after the first scan of any repo you'll revisit. |
| List registered repos | `arch_registry_list` | Inventory. Use when the user says "what repos are registered". |
| Remove an alias | `arch_registry_remove` | Cleanup. Use for "forget that repo". |

## Worked examples

### Example 1: "What would break if I remove the cache layer?"

This is a blast-radius question, not a scan question. Call `arch_blast_radius` with the cache node ID. If the user hasn't identified the cache node yet, call `arch_scan` first to find its ID (look for nodes of type `cache` in the result), then pass that ID to `arch_blast_radius`.

```
arch_scan(path: "/path/to/repo")              # locate nodes of type=cache
arch_blast_radius(path: ".", target: "infra:cache")
```

Present the result as a list of affected modules with edge types.

### Example 2: "Show me the architecture of this MCP server."

Two-step: scan, then generate a diagram. Default to Mermaid; ask if they want a different format only if they hint at one ("for our docs site", "for a PowerPoint").

```
arch_scan(path: ".")
arch_generate(path: ".", format: "mermaid")
```

If the result has more than ~30 nodes, suggest `arch_focus` on a subdirectory instead — diagrams of huge repos are unreadable.

### Example 3: "Has our architecture drifted since v1.0?"

Single call:

```
arch_drift(path: ".", base_ref: "v1.0", head_ref: "HEAD")
```

If the user asks "why did it drift", follow up with `arch_drift_explain` using the same refs — that returns prose; `arch_drift` returns the delta.

## Gotchas

- **Diagrams of huge repos are unreadable.** If `arch_scan` returns more than ~30 nodes, default to `arch_focus` on the area the user actually cares about before generating a diagram.
- **`arch_validate` with a repo-local `.arch-rules.yaml`** ignores the file unless `RIDGE_ALLOW_INREPO_RULES=1` is set in the environment (confused-deputy protection). Tell the user to set the env var if they want repo-local rules honored.
- **`arch_drift` between two refs scans both refs.** It's slower than `arch_diff` against a saved snapshot. Use `arch_snapshot` once and `arch_diff` repeatedly when iterating.
- **Markdown analysis is wiki-vault-shaped.** Ridge extracts Obsidian `[[wiki-links]]` and relative `.md` links. It is not a general-purpose Markdown understander; don't expect it to summarize prose.
- **Imports are not connections.** Analyzers detect import declarations, not actual call-site usage. A module that imports `redis` but never uses it will still show a cache edge. Treat the graph as a high-confidence upper bound.
- **Oversized source files are stubbed.** Files past 5 MB return a placeholder node with `skipped=size`. This is intentional (DoS protection); ridge does not parse them.

## When not to use ridge

- Single-function cyclomatic complexity or code-quality scores — use `gocyclo`, `golangci-lint`, or CodeScene.
- Runtime tracing or profiling — out of scope; ridge is static analysis only.
- Documentation generation from comments — ridge extracts structure, not API docs.
- Anything that needs the LLM to read the source code itself — ridge returns graphs; the LLM should consume those, not be replaced by them.
