# Ridge Constitution

This document holds the governance articles for Ridge. These articles are **non-negotiable** and **not subject to per-feature override**. They apply to every commit, pull request, and release regardless of urgency or scope.

This document does not change without an explicit constitutional amendment: a dedicated pull request that modifies only this file, reviewed by the maintainer. A feature pull request that would violate an article does not get an exception; it either changes to comply, or it waits behind an amendment.

**Every article below codifies something the repository already does.** No article invents a new requirement. Each names the file or pattern it is drawn from, and each states honestly whether a linter, a test, or a CI job enforces it, or whether it rests on review alone. An article that claims enforcement it does not have is worse than one that admits it has none, because the false claim stops anyone from adding the missing check.

Written 27-08-2026 against `main` at go-sdk v1.7.0, 19 registered tools.

---

## Article I: Tool registration is declarative and single-entry

Adding a tool means adding one `ToolSpec` to `AllTools` in `tools/definitions.go` and one entry to the `registrars()` map in `tools/handlers.go`. Handlers MUST NOT be registered by hand-written boilerplate: they go through the generic `register[Args, Result]`, which is what attaches panic recovery and the uniform error-wrapping (`"%s failed: %w"`) to every tool. A tool wired around `register` gets neither and MUST NOT be merged.

Every spec MUST carry a non-empty `Name`, `Method`, `Title`, `Category`, and `Description`, and every description MUST contain a `USE WHEN` clause. Names begin with `arch_`. Names and methods are unique.

Why: 19 tools with 19 hand-rolled registrations would drift into 19 slightly different recovery and error behaviours. The single generic wrapper is the reason a claim like "every handler recovers from panics" can be true at all.

Codifies: `tools/definitions.go` (`ToolSpec`, `AllTools`), `tools/handlers.go` (`RegisterAll`, `registrars`, `register`).

**Enforcement: mechanically checked, with one hole.** `tools/definitions_test.go` runs `TestAllToolsHaveRequiredFields`, `TestAllToolsHaveExpectedCount` (pinned at 19), `TestAllToolDescriptionsHaveUSEWHEN`, `TestToolNamesAreUnique`, and `TestToolMethodsAreUnique`, all in CI via `go test -race` in `.github/workflows/ci.yml`. The hole: `RegisterAll` skips any spec whose `Method` has no registrar entry (`tools/handlers.go:57-61`, the `if ok` branch) and nothing fails. The server starts with that tool silently missing from `tools/list`; `TestSchemaPropertiesAreDescribed` opens a live session but only asserts the tool list is non-empty, not that it has 19 entries. A registrar-coverage test is the smallest change that would close this.

---

## Article II: Handlers never panic out

Every tool handler runs behind `defer h.recoverPanic(spec.Name, &retErr)` inside `register` (`tools/handlers.go:174`), and the enclosing function MUST use **named return values**. Without named returns the deferred reassignment is a no-op and a recovered panic reaches the caller as a zero-valued success, which an agent reads as a valid empty response. The panic value and stack are logged server-side; only a correlation ID reaches the MCP caller.

Why: a fake-success response is the most expensive failure an agent can receive, because it has no signal to retry or report on. The correlation ID keeps stack traces out of the caller's context window while leaving them recoverable from the server log.

Codifies: `tools/recover.go` (`recoverPanic`, `newCorrelationID`, and the MUST-use-named-returns comment at `tools/recover.go:15-17`), `tools/handlers.go` `register` (the single wrapper with named returns).

**Enforcement: partially mechanical.** `tools/recover_test.go` runs `TestRecoverPanic_AssignsErrorWithCorrelationID`, `TestRecoverPanic_NoPanicNoError`, `TestRecoverPanic_PreservesExistingError`, and `TestNewCorrelationID_DistinctAndShaped`. Nothing checks that a future second registration path would use named returns; that rests on `register` being the only path (Article I) and on review.

---

## Article III: Every caller-supplied path and git ref is validated before use

A tool argument that names a filesystem location MUST pass through `internal/safepath` before anything reads or writes it. `ValidateScanPath` resolves the path (`Abs` + `EvalSymlinks`), refuses the sensitive-root denylist (`/etc`, `/proc`, `/sys`, `/dev`) and the home-directory credential dirs (`.ssh`, `.gnupg`, `.aws`, `.config/gcloud`), and, when `RIDGE_ALLOWED_DIRS` is set, refuses anything outside the allowlist. `ValidateOutputPath` confines writes to the scanned repo, rejecting both symlink escapes and the `HasPrefix` sibling trick. The posture is announced at startup (`cmd/ridge/main.go:50`, `announceAllowlistPosture`) so an operator can see what is enforced without reading code.

The same discipline covers subprocess arguments: a git ref MUST pass `drift.ValidateRef` (rejects empty refs, leading dashes, and shell metacharacters) before it reaches `exec.CommandContext` in `internal/drift/gitref.go` or `history.go`. `arch_validate`'s `baseline_file` is likewise containment-checked (`baselineFilePath`, `tools/handlers_validate.go:92-95`).

Why: this server's arguments come from an agent, and an agent's arguments can come from anywhere, including scanned content. Path containment is the guardrail the agent cannot be talked out of.

Codifies: `internal/safepath/safepath.go`; the 19 `safepath.Validate*` call sites across `tools/handlers_scan.go:164,234`, `tools/handlers_deps.go:38,162`, `tools/handlers_render.go:45`, `tools/handlers_dataflow.go:35,110`, `tools/handlers_meta.go:38,51,88,127,180`, `tools/handlers_drift.go:29,35,74`, `tools/handlers_registry.go:31`, `tools/handlers_validate.go:47,101,174`; `internal/drift/gitref.go:13-28`.

**Enforcement: mechanically checked for the validators, review for the call sites.** `internal/safepath/safepath_test.go` runs `TestValidateScanPath_SensitiveSystem`, `TestValidateScanPath_SensitiveDotDirs`, `TestValidateOutputPath_RejectsSymlinkEscape`, `TestValidateOutputPath_RejectsHasPrefixSiblingTrick`, and the five `Allowlist` tests; `internal/drift/gitref_test.go` runs `TestValidateRef_Valid`, `TestValidateRef_Empty`, `TestValidateRef_DashPrefix`, `TestValidateRef_InvalidChars`, and `TestCheckoutRef_InvalidRef`. Nothing forces a new handler to call `ValidateScanPath`; that is a review responsibility. The clean-clone CI job exists because exactly this package was once referenced from uncommitted local state (Article XII).

---

## Article IV: Scanned source never leaks secrets into responses

Sample snippets flow to MCP callers via `Node.Source` (`arch_scan` with `detail="full"` and `samples=true`), so a hardcoded key in scanned source would otherwise be echoed verbatim into an agent's context. Every snippet passes through the redaction pass in `internal/scanner/mask.go` before it reaches a node: key-named assignments (`api_key`, `secret`, `token`, `password`, ...), `Bearer` credentials, well-known token shapes (`sk-`, `ghp_`, `github_pat_`, `xox[bp]-`, `AKIA`), and high-entropy quoted assignment values are replaced with `[REDACTED]`.

This is incident-shaped rather than aspirational: masking landed in commit `3ccc2d3` (11-07-2026, PR #29) precisely because samples shipped first and the leak path was noticed after.

The redaction is regex plus entropy and therefore best-effort by construction. The binding rule is narrower and checkable: any new response field that carries scanned file content MUST route through the same mask before leaving the server.

Codifies: `internal/scanner/mask.go`, `internal/scanner/samples.go` (the population path), commit `3ccc2d3`.

**Enforcement: mechanically checked for the existing path.** `internal/scanner/mask_test.go` runs `TestMaskSecrets` and `TestPopulateSamples_MasksSecrets`. Nothing detects a future field that bypasses the mask; that rests on review.

---

## Article V: Errors are never silently discarded

An operation MUST NOT swallow an error. If an error cannot be handled where it occurs, it is logged with enough context to identify the failing object and surfaced to the caller. A best-effort path that tolerates an error MUST say so at that line: `NewHandlerRegistry` warns and degrades to a nil registry when `registry.Load` fails (`tools/handlers.go:39-42`), after which registry-dependent calls return an explicit `"repo registry not available"` error rather than nil-panicking; sample population tolerates an unreadable file by caching a nil entry, because samples are decoration, not data.

The receipt for why absence-of-signal is the failure mode to fear is Article XIII's tracing incident: a dependency bump left the OTel wiring dead for weeks with CI green, and the symptom was nothing, anywhere.

Codifies: `tools/handlers.go:39-42`, `internal/scanner/samples.go:80-85`, `.golangci.yml` enabled list.

**Enforcement: mechanically checked.** `errcheck` is in the enabled linter list in `.golangci.yml` (alongside `gosec`, `govet`, `ineffassign`, `staticcheck`, `unused`) and runs on every pull request via the `lint` job in `ci.yml` (golangci-lint v2.7.2). Verified locally 27-08-2026: `golangci-lint run ./...` reports **0 issues**. The config's `std-error-handling` exclusion preset deliberately tolerates the conventional discard sites; a bare `golangci-lint run --no-config --default=none --enable=errcheck ./...` probe on the same date reports the four sites that preset covers, two of them in production code: `internal/drift/gitref.go:59` (`os.RemoveAll(tmpDir)` on the failed-worktree cleanup path, where the worst case is a leftover temp dir) and `internal/scanner/samples.go:85` (`defer f.Close()` on a file opened read-only). The other two are in `internal/safepath/safepath_test.go:35-36`. All four are the discard-is-safe class, and the preset that admits them is a stated policy in `.golangci.yml`, not an accident. The `gosec` `G104` exclusion's comment "Covered by errcheck" is, in this repository, actually true.

---

## Article VI: Arguments are validated first, and errors name the recovery

Every handler validates its arguments before doing work, and every validation error tells the caller what to do instead. `arch_generate` on an unknown format returns the full supported list (`tools/handlers_render.go:142`); a missing path returns `"either path or repo is required"`; `paths` and `path`/`repo` together is a validation error by declared contract; an invalid baseline mode names the two valid values (`tools/handlers_validate.go:78`). Every tool description carries a `FAILS WHEN` section stating the known failure conditions and, where applicable, the retry.

Why: an agent recovers from an error exactly as well as the error text lets it. `"unsupported format: foo (supported: mermaid, plantuml, ...)"` costs one retry; `"invalid input"` costs a guessing loop.

Codifies: `tools/handlers.go` (`resolveRepoPath`), `tools/handlers_render.go:142`, `tools/handlers_validate.go:78`, the `FAILS WHEN` sections throughout `tools/definitions.go`.

**Enforcement: mechanically checked per tool family.** The `_RejectsBadInput` convention: `TestArchRegistryAdd_RejectsBadInput`, `TestArchRegistryRemove_RejectsBadInput` (`tools/handlers_registry_test.go`), `TestArchDiff_RejectsBadInput`, `TestArchDrift_RejectsBadInput` (`tools/handlers_drift_test.go`), and their siblings across the handler test files. Nothing checks that a `FAILS WHEN` section stays accurate as code changes; that is review.

---

## Article VII: Every response is bounded and honest about truncation

No tool returns an unbounded payload. `arch_scan` returns a summary by default; `detail="full"` is opt-in and its node and edge slices are clipped to `maxFullDetailNodes = 1000` (`tools/handlers_scan.go:277`), overridable per call, with `Truncated: true` set whenever anything was dropped. Scans themselves are bounded: `ScanControl` exposes `max_files`, `max_nodes`, a `timeout_secs` defaulting to 120 seconds, and a worker count capped at 32, and any hit limit marks the result truncated (`internal/scanner/scanner.go:53`). These numeric values are the contract; raising a default is an amendment, not a feature change.

Why: a caller pays for an oversized response twice, once on the response and again on every following turn that carries it forward. A clipped graph with no flag is worse than an error, because the caller reasons about a partial architecture believing it complete.

Codifies: `tools/handlers_scan.go` (`clipFullDetail`, `maxFullDetailNodes`, `ArchScanResult.Truncated`), `tools/handlers.go:100-129` (`ScanControl`, `toScanOptions`), `internal/scanner/scanner.go` (`ScanResult.Truncated`, `withOptionalTimeout`).

**Enforcement: partially mechanical.** `TestClipFullDetail` in `tools/handlers_scan_hg2_test.go` covers the clip-and-flag behaviour, including the under-cap and explicit-limit cases. Nothing asserts that a new tool returning graph data applies a cap; that rests on review, and it is the article most likely to erode quietly.

---

## Article VIII: A tool description is a public contract with the agent

The description on a `ToolSpec` is the only thing an agent reads before deciding whether to call a tool. Changing it changes behaviour for every caller, invisibly, with no version bump and no error. Descriptions follow the established shape and keep it: a first line stating what the tool does, then `USE WHEN`, then the applicable `WHY` and `FAILS WHEN` sections. Cross-references to sibling tools ("For a single service or subdirectory, use arch_focus instead") are load-bearing disambiguation and MUST NOT be dropped when a description is shortened. Every input-schema property carries a description, because an undescribed property forces the client to guess from the name alone; commit `f42d46e` (PR #49) paid down 131 undescribed properties at once, which is what happens when this rule is enforced late instead of early.

**Named standing violation.** The `serverInstructions` block in `cmd/ridge/main.go` is part of this same contract and is currently stale: it claims three supported languages where the server analyzes six substrates (Go, TypeScript, Python, Rust, Java, Markdown) and lists seven output formats where nine exist. Nothing tests that constant against the code. It should be fixed and, ideally, derived or tested rather than hand-maintained.

Codifies: `tools/definitions.go` (every spec), `cmd/ridge/main.go` (`serverInstructions`), commit `f42d46e`.

**Enforcement: partially mechanical.** `TestAllToolDescriptionsHaveUSEWHEN` checks the shape's anchor clause. `TestSchemaPropertiesAreDescribed` (`tools/schema_descriptions_test.go`) opens a real in-memory MCP session and asserts every property of every listed tool's input schema carries a description, deliberately reading what a client actually receives rather than the server-side structs. Nothing checks that a description edit preserved its cross-references, and nothing checks `serverInstructions` at all.

---

## Article IX: Annotations tell the truth about what a tool does

`ReadOnly` and `Idempotent` on a `ToolSpec` become MCP tool hints that clients use to decide whether to prompt a human. A tool MUST NOT set both: idempotence carries meaning only for tools that change state, and asserting it on a read misleads a client reasoning about retry safety. Where a non-read-only tool is genuinely convergent, the hint is right: `arch_validate` carries `Idempotent: true` alongside a comment stating why it is not read-only (`baseline="write"` saves a known-violations file), and `arch_snapshot` is marked non-read-only with the reason in a comment and a `WARNING` about overwriting in its description.

This article is incident-born: commit `7b4c743` (PR #50) stripped `idempotentHint` from all 15 read-only specs, which had carried it since their creation, and added the coherence test in the same change.

**Bounded scope.** `ToolSpec` has no `Destructive` field, deliberately: this server's mutations are confined to its own state under `~/.mcp-context/` (registry, scan state, snapshots) and to repo-contained output files behind `ValidateOutputPath`. `arch_registry_remove` deletes scan state that the next scan regenerates. A future tool that can destroy user data does not reuse this scope; it adds the hint field and a coherence check first, via amendment.

Codifies: `tools/definitions.go` (annotation fields and the two commented non-read-only specs at lines 177-178 and 202), `tools/handlers.go` `register` (the single place annotations become wire hints), commit `7b4c743`.

**Enforcement: mechanically checked.** `TestAnnotationCoherence` in `tools/annotation_coherence_test.go` fails on any spec setting both `ReadOnly` and `Idempotent`, and its comment records the reasoning. Runs in CI. This is the best-enforced small article in the document.

---

## Article X: Cache hints are explicit, never SDK defaults

The MCP 2026-07-28 revision requires `ttlMs` on list results, and the go-sdk fills it with 0, which the spec defines as immediately stale; a compliant client would re-fetch the tool list every turn. This server therefore stamps its own policy through `mcpcache.Middleware`: `tools/list` and `server/discover` advertise a one-hour TTL, because the tool set is fixed at compile time. The default for every other method stays zero **on purpose**: a blanket TTL would also cover live-content reads, so methods are named explicitly (`cmd/ridge/main.go:84-101`, `toolListTTL`, `cacheConfig`).

Why: the SDK's required-field default is spec-compliant and useless. Shipping a useful value is a deliberate act, and which methods get it is a correctness decision, not a tuning knob.

Codifies: `cmd/ridge/main.go` (`toolListTTL`, `cacheConfig`, the `AddReceivingMiddleware` call in `newServer`), commits `577a1ab` and `3421c5f`, the `mcp-cache-go` direct dependency in `go.mod`.

**Enforcement: none mechanical in this repository.** No test here asserts the advertised TTL; the middleware's behaviour is tested in `mcp-cache-go`, and `cmd/ridge/main.go` has no test file. Rests on review.

---

## Article XI: Incremental scan state is a cache, never a source of truth

`ScanState` records per-file mtime and content hash plus the producing analyzer's `Signature()`, and unchanged files reuse cached nodes and edges. Three properties make that reuse safe, and they are the contract: an mtime change alone does not invalidate (the content hash is checked, so a `touch` costs a hash, not a re-parse); a hash failure invalidates conservatively (`internal/scanner/filestate.go:104`, re-analyze rather than trust); and a changed analyzer signature invalidates every file that analyzer produced, so an analyzer upgrade can never serve stale shapes from a previous parser. State files under `~/.mcp-context/` are disposable: `arch_registry_remove` deletes them, and the next scan rebuilds from source.

Why: this repository is the portfolio's reference implementation for incremental scan state. A cache that can survive its own producer changing is not a cache; it is a corruption vector with good latency.

Codifies: `internal/scanner/filestate.go` (`ScanState`, `DetectChanges`, `classifyWalkedFile`, `UpdateFile`), `internal/scanner/scanner_incremental.go`, `internal/registry/registry.go` (`StatePath`), `internal/infra/persist.go`.

**Enforcement: mechanically checked.** `internal/scanner/filestate_test.go` runs `TestDetectChanges_NewFiles`, `TestDetectChanges_UnchangedFiles`, `TestDetectChanges_ModifiedContent`, `TestDetectChanges_TouchedButSameContent`, `TestDetectChanges_DeletedFiles`, `TestUpdateFile_StoresAnalyzerSignature`, `TestClassifyUnchanged_SignatureInvalidation`, and `TestClassifyUnchanged_SignatureMatch`. All run in CI.

---

## Article XII: The supply chain is verified on every pull request, from a clean clone

CI MUST verify, on every push to `main` and every pull request against it: module checksums (`go mod verify`), that neither `go.mod` nor `go.sum` drifts from `go mod tidy` (the diff covers **both** files, which is deliberate: a stale `// indirect` annotation passes build, test, and a `go.sum`-only diff), `gosec` with the same exclusion set as `.golangci.yml`, `go vet`, tests with the race detector, and `golangci-lint` in a separate job.

The distinctive step is `clean-clone`: an independent job that builds and tests from a pristine `git clone` with no caching. It is incident-born, and the workflow comment carries the receipt: commit `e346a99` referenced `safepath.ValidateOutputPath` from uncommitted local state, compiled fine on the contributor's machine, and went undetected until the 22-04-2026 SDK bump sweep (fixed in `b8fbdf9`). A CI whose checkout inherits nothing from the developer's disk is what makes "main builds" a fact rather than a local impression.

Codifies: `.github/workflows/ci.yml` (`test`, `build`, `lint`, `clean-clone` jobs), commit `e24cf7d` (pinning why the lint job skips the remote schema verify).

**Enforcement: mechanically checked, with one step that cannot fail.** `govulncheck` runs with `|| echo "::warning::..."`, so a known vulnerability produces a warning annotation and a green build; the stated rationale (stdlib findings resolve with Go patch updates) is real, and the cost is that a vulnerable direct dependency is equally unable to redden the build. Coverage upload is `fail_ci_if_error: false`, correct for a reporting step. Everything else fails the build.

---

## Article XIII: Tracing is optional and non-fatal, and its wiring executes under test

OpenTelemetry setup failures MUST NOT take the server down: `setupTracing` warns and returns a no-op shutdown (`cmd/ridge/main.go:63-79`). That tolerance is exactly what makes silent breakage possible, so it carries a matching obligation: `tracing.Setup` MUST be executed by a test, not merely compiled. The semconv schema pin (`go.opentelemetry.io/otel/semconv/v1.43.0`, `tracing/tracing.go:15`) breaks on every OTel minor that moves the schema URL, and before commit `e2d47bb` this repository had no test executing `Setup`, so the 1.44 bump left tracing dead with CI green and no symptom anywhere. The regression test added in that commit is what turns the next such break from silence into a red build.

Why: Article V forbids swallowed errors; a deliberately-tolerated subsystem is the one place a swallowed error is policy, so the detection has to move somewhere else. Here it moved into the test suite.

Codifies: `tracing/tracing.go` (the semconv pin, `Setup`), `cmd/ridge/main.go` (`setupTracing`), commit `e2d47bb` (PR #54).

**Enforcement: mechanically checked.** `tracing/tracing_test.go` runs `TestSetup_EnabledWithStdout`, `TestSetup_DifferentSampleRates`, and `TestSetup_Disabled`, all of which execute `Setup` and therefore the semconv resource construction, in CI. Bumping the pin without running the suite is no longer a silent operation.

---

## Articles considered and rejected

**Anything that does I/O takes `context.Context` first.** A near-universal article in this portfolio's sibling constitutions, rejected here because it is not what this repository does and an honest exemption list would swallow the rule. The `Analyzer` interface is `Analyze(path)` with no context (`internal/scanner/analyzer.go:8`), across all six analyzers, by design: cancellation lives one level up, in `Scanner.ScanWithOptions(ctx, ...)` and `withOptionalTimeout` (`internal/scanner/scanner.go:115,192`), and the git subprocesses use `exec.CommandContext`. The enforceable form of the intent, every scan is deadline-bounded with a 120-second default, is already Article VII.

**Semantic versioning, and the changelog is part of the change.** Rejected because there is nothing versioned to defend: no `CHANGELOG.md`, no git tags, no GitHub releases, and `ServerVersion` is a static `"0.1.0"`. Distribution is `go install .../cmd/ridge@latest` straight off `main`. The article would be a wish. If releases ever ship, this is the first amendment to propose, and the definition of "breaking" should start from Article VIII's description contract.

**No credentials in version control.** Rejected as codifying a risk this server does not carry: it wraps no API, holds no tokens, and has no auth surface, so there is no credential to commit. `gosec`'s `G101` is excluded in `.golangci.yml` for false positives, meaning no scanner stands behind such an article either. The credential risk this repository actually has is echoing *other people's* secrets out of scanned source, and that is Article IV.

**Fixtures are captured from live responses, never imagined.** Rejected as not applicable in shape: there is no third-party API whose wire format could be imagined wrong. Analyzer fixtures are hand-written source files, which are the input domain itself, and `tests/integration_test.go` (`TestScanSelf`, `TestScanLocalGoRepos`, `TestScanClonedTSRepos`) scans real repositories, including this one, which is the honest equivalent of a live probe.

**Test coverage on every exported function.** Rejected because it is not true and stating it would make the document a wish. Coverage is real and dense across `tools/`, `internal/scanner/`, `internal/drift/`, and `internal/safepath/`, but `cmd/ridge/main.go` (228 lines, including the cache policy of Article X and the stale `serverInstructions` of Article VIII) has no test file at all. That gap is worth a bead, not an article.

**Structured logging with `log/slog` everywhere.** Rejected because it would constrain one line: the repository already uses `slog` throughout, and the sole exception is `log.Fatalf` on transport failure at `cmd/ridge/main.go:143`, where the process is exiting anyway. An article that exists to police a single defensible call site is ceremony.

**Operations that grant durable access fail closed.** Rejected as not applicable: no tool shares, invites, grants a role, or writes a credential. The fail-closed discipline this server genuinely practices is path and ref containment, which stands as Article III with its own test files rather than being generalized into a principle borrowed from servers that have sharing surfaces.

---

## Amendment log

| Date | Change |
|------|--------|
| 27-08-2026 | Ratified. Thirteen articles, adapted from the `CONSTITUTION.md` in `gridctl/gridctl` (Apache-2.0, github.com/gridctl/gridctl). |
