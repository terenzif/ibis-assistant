# Changelog - May 7, 2026

This document summarizes implementations between May 6 and May 7, 2026. For **September 2026** work (RecordID, ask ranking, `sync_local_patch`, log enrich/report, AI rule synth, polyglot globs), see **[changelog_20260918.md](changelog_20260918.md)**.

## 1. Dynamic Workspace Management and Code Ingestion
- **Dynamic Code Ingestion**: Added `internal/ingest/dynamic/queue.go` and `workspace.go` to orchestrate real-time repository updates via `ProjectIngestionManager`. Synchronization (fetch/checkout) is separated from asynchronous vectorization.
- **File Discovery with `git ls-tree`**: Major refactor of `internal/ingest/code/ingest.go`. The Git tree (HEAD) is now the true Single Source of Truth for file discovery, replacing obsolete recursive filesystem traversal.
- **Smart Pruning**: Obsolete file removal uses the `activeFilesMap` extracted from Git, automatically removing from the database all chunks associated with files no longer present in the current commit tree.

## 2. Deterministic Log Analysis (LogAlign) and Smart Chunking
- **LogAlign Methodology**: Updated `internal/ingest/logs/analyzer.go` to intercept logs using static templates extracted from code. This bypasses costly and unpredictable LLM-based analysis whenever a log matches a known signature.
- **Extended Batch Manager**: Updated `internal/ai/batch_manager.go` to optimize context windows and manage AI-assisted Smart Chunking without splitting stack traces.
- **Database Schema**: Introduced new nodes and edges in `internal/schema/schema.go` (`TableLogTemplate` and `EdgeEmitsLog`) to relate log lines deterministically to the exact source lines that emitted them.

## 3. Ast-Grep Sidecar Integration
- **Sidecar Engine**: Created `internal/ingest/code/ast_chunker.go` to orchestrate syntactic code extraction delegated to the Rust executable `sg.exe` (ast-grep), avoiding native dependencies.
- **Multi-Language YAML Rules**: Chunking logic for C#, Go, JS, TS, Java, Python, and C++ is now encoded in `rules/*.yml` together with `sgconfig.yml`. Extending the parser no longer requires recompilation.

## 4. Search Engine and Agentic Cleanup
- **Deprecation of Experimental Models**: Removed `internal/optimization/raft_test.go`, `internal/search/reinforce_test.go`, and failed experimental modules in favor of a solid multi-modal Agentic implementation.
- **Agentic Search**: Refactor in `internal/search/agentic.go` and `search.go` to harden agentic search, backed by multiple unit tests (parsing, regex, robustness, multiline).

## 5. Unified Deployment and Configuration (`dist/`)
- **Master Config**: Elected `config_master.json` as the single version-controlled configuration file. Removed accidental key leaks from the old `config.json`.
- **`make dist` Target**: The `Makefile` now automatically assembles the full production environment into a `dist/` directory.
- **New System Paths**: Process directories were unified and renamed without underscores: now `dist/db`, `dist/logs`, and `dist/repos`.
- **Root Cleanup**: Removed dozens of temporary files from the root (`patch.diff`, `temp.go`, `test.cs`, old `.bat` files, and Copilot exports).
