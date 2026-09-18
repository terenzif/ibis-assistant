# Changelog — 17–18 September 2026

Shipped on `terenzif/ibis-server` `master` (merge tip `3b3d3ee`). Primary docs are English. This note records **what landed**, **where it lives**, and **how it was verified**.

## Stack merged to master

| PR | Branch | Commit (feature tip) | Summary |
|----|--------|----------------------|---------|
| [#1](https://github.com/terenzif/ibis-server/pull/1) | `fix/recordid-embed` | `198111a` + `5be666e` | RecordID formatting / embedding batch persistence; ignore local `AGENTS.md` |
| [#2](https://github.com/terenzif/ibis-server/pull/2) | `fix/ask-project-search` | `4fc8a08` | Ask/project hybrid ranking and coverage (commits landed via later stack merges) |
| [#3](https://github.com/terenzif/ibis-server/pull/3) | `feat/sync-local-patch` | `6225b2c` | MCP `sync_local_patch` for unpushed local commits |
| [#5](https://github.com/terenzif/ibis-server/pull/5) | `feat/logs-pipeline` | `4cab006` | Enrich, LogAlign promote, sidecar reports (commits landed via #4) |
| [#4](https://github.com/terenzif/ibis-server/pull/4) | `feat/ast-rule-synth` | `570d06a` | AI ast-grep rule synthesis + polyglot languageGlobs |

> Note: PR #2 and #5 were closed when intermediate base branches were deleted during stacked merge; their commits are ancestors of `origin/master` (`4fc8a08`, `4cab006`).

## Features (code → docs)

### 1. Safe Surreal RecordIDs

- **Code:** `internal/db/utils.go` — `FormatRecordID`, `CoerceRecordID`, `IsSafeRecordID`
- **Why:** Avoid broken SurrealQL when IDs contain special characters; stabilize embedding batch persistence paths that round-trip record IDs
- **Tests:** `internal/db/utils_test.go`

### 2. Ask / project hybrid search

- **Code:** `internal/search/search.go` (`AskProject` / agentic entry from MCP `ask_project`)
- **Behavior:** Surreal multi-table `FROM [a,b,c]` is unreliable for vector queries here, so each table (`file_chunk`, memory, reasoning) is queried separately and scores are merged in Go with a time-decay term
- **MCP:** `ask_project(query, branch_or_commit?)`
- **Tests:** `internal/search/*_test.go` (unit / agentic / integration suites under `internal/search/`)

### 3. `sync_local_patch`

- **Code:** `cmd/server/main.go` MCP tool `sync_local_patch`
- **Flow:** After `init_project`, if the tip is not on the remote, the server may return `requires_patch` with `closest_known_commit`. The client supplies a unified diff via `sync_local_patch(project_name, patch, commit?)` so the workspace aligns without pretending a remote commit exists
- **CLI:** MCP-only today (no `ibis-assistant sync` subcommand)

### 4. Log analysis pipeline (enrich + report + AST promote)

- **Code:** `internal/ingest/logs/` — `watcher.go`, `tailer.go`, `analyzer.go`, `enrich.go`, `logalign.go`, `reporter.go`, `ast_rule_gen.go`
- **Flow (high level):** recursive watch under `logs_root` → chunk/tail → **ast-grep / LogAlign / known patterns first** → AI only on residual → enrich (file / cause / baseline) → promote AI hits into LogAlign templates → optional AI→ast-grep YAML for logs → markdown report beside the log
- **MCP / CLI:** `analyze_logs` / `ibis-assistant logs analyze --project … --text … [--file …]`
- **Channels still valid:** fsnotify watcher, `POST /api/v1/logs/upload`, polling, MCP
- **Architecture doc:** [log_analysis_pipeline.md](log_analysis_pipeline.md)
- **Tests:** `analyzer_test.go`, `enrich_test.go`, `logalign_test.go`, `ast_rule_gen_test.go`

### 5. Polyglot code ingest + AI ast-grep rule synthesis

- **Code:** `internal/ingest/code/` — `rule_synth.go`, `sparse_ast.go`, `polyglot.go`, `lang_globs.go`, `ast_chunker.go`, `install.go`
- **Config:** root `sgconfig.yml` `languageGlobs` (ASPX/Vue/ERB/… → HTML host so embedded regions parse)
- **Behavior:** when static rules are sparse for a language/extension, AI may propose validated `rules/ai-generated-*-{chunk|calls|logs}.yml` and extend `languageGlobs` at runtime
- **Architecture doc:** [code_ingest_polyglot_and_rules.md](code_ingest_polyglot_and_rules.md)
- **Tests:** `rule_synth_test.go`, `ast_chunker_test.go`

## Verification evidence (as of this changelog)

| Check | Result / pointer |
|-------|------------------|
| Unit tests (db / search / logs / code) | Covered by packages listed above; run `go test ./internal/db/ ./internal/search/ ./internal/ingest/logs/ ./internal/ingest/code/` |
| MCP tool registration | Source of truth: `cmd/server/main.go` (`ask_project`, `sync_local_patch`, `analyze_logs`, `ingest_code`, …) |
| Stacked merge on GitHub | PRs #1–#5 on `terenzif/ibis-server`; tip `3b3d3ee` |
| Manual QA (this repo as subject) | Log watch/analyze exercised against the project itself; reports expected beside the watched log with file/cause when enrich succeeds |

## Doc coherence follow-ups applied with this changelog

- README: features, CLI `logs analyze`, clone path, links to new pipeline docs
- `CLAUDE_INTEGRATION.md` / `copilot-instructions.md`: current MCP inventory (removed obsolete tool names)
- Log/CLI proposals: status aligned with shipped code; `analyze_log_stream` → **`analyze_logs`**
- Deprecated hybrid-search stub doc: banner updated (AskProject is live)

## Out of band (local only)

- `AGENTS.md` is **gitignored** and must not be committed
- Do not document archive remotes or pre-rewrite branch tips in committed docs
