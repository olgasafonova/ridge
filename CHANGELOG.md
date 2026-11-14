# Changelog

All notable changes to ridge are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/). Before 1.0.0, a minor version can
change tool arguments or result fields.

## [Unreleased]

## [0.1.0] - 14-11-2026

First tagged release. Until now `go install ...@latest` built whatever was on
`main`; it now resolves to this tag.

### Tools (19)

- **Analysis:** `arch_scan`, `arch_focus`, `arch_dependencies`,
  `arch_blast_radius`, `arch_dataflow`, `arch_boundaries`, `arch_explain`
- **Diagram:** `arch_generate`
- **Drift:** `arch_diff`, `arch_drift`, `arch_drift_explain`
- **Validation:** `arch_validate`, `arch_metrics`, `arch_recommend`
- **History:** `arch_history`
- **Export:** `arch_snapshot`
- **Registry:** `arch_registry_add`, `arch_registry_remove`, `arch_registry_list`

### Languages

- Go, through `go/ast`
- TypeScript, Python, Rust and Java, through tree-sitter
- Markdown: Obsidian wiki-links and relative `.md` links

### Output formats

Mermaid, PlantUML, C4, Structurizr DSL, JSON, draw.io, Excalidraw,
self-contained HTML, and a D3 force-directed graph (`forcegraph`).

### Highlights of the work leading up to this tag

- `arch_generate` caps the diagram at 100,000 characters by default. A
  longer one comes back with `truncated: true`, `total_chars`, and a note
  naming the `max_chars` that returns it whole (#77).
- `arch_generate` takes `group_depth` to collapse files into one box per
  folder, for module graphs too large to draw file by file (#71).
- Scan controls (`max_files`, `max_nodes`, `skip_dirs`, `skip_globs`,
  `include_tests`, `timeout_secs`, `workers`) on the scanning tools, including
  `arch_drift` and `arch_history` (#72, #73, #74).
- A `scan` subcommand runs one scan to JSON without an MCP client: `ridge scan <path>` after `go install`, `code-to-arch scan <path>` after `make build` (#72).
- Root-relative, stable node IDs for Go, Python, Rust, Java and TypeScript,
  so drift between two refs compares like with like (#69, #73, #74).
- TypeScript: real route extraction, SvelteKit and Svelte support, SvelteKit
  form actions (#71, #73, #76). Java: imports of project classes link to
  their modules (#75).
- `arch_validate` baseline write/check modes, so known violations can be
  ratcheted down instead of failing every run (#31).
- `arch_recommend` and `arch_boundaries` return graph-derived evidence and a
  confidence for each finding (#25, #28).
- Secret-shaped content is masked in sample snippets before it reaches a
  node's source (#29).
- `RIDGE_ALLOWED_DIRS` restricts which directories may be scanned.
- `tools/list` advertises a one-hour cache TTL (#46).

[Unreleased]: https://github.com/olgasafonova/ridge/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/olgasafonova/ridge/releases/tag/v0.1.0
