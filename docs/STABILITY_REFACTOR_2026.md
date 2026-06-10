# Architecture Stability Refactor (2026-03)

This document summarizes the major technical refactorings implemented to enhance the stability, reliability, and deployability of the Ibis Arc on Windows.

## 1. Context Propagation Layer
To support graceful shutdowns and robust cancellation of high-load operations, the entire AI and Database communication layer was refactored for consistent `context.Context` propagation.

- **`db.Executor` interface**: Updated to require `context.Context` for all methods (`Execute`, `SmartQuery`).
- **AI Worker (`gemini.go`)**: Now uses a scoped context for background flushing and rate-limiting, ensuring no orphaned API calls remain during shutdown.
- **`BatchManager`**: Fully integrated with the system's supervisor context to manage background embedding jobs.

## 2. Windows Service & Lifecycle Management
Multiple critical fixes were applied to address service mode failures and lifecycle issues.

- **Graceful Shutdown**: Resolved "TCP connection reset" errors (WSARECV) during shutdown by implementing proper signal handling and ensuring the database executor is closed only after all workers have terminated.
- **Service Entry Point**: Fixed a bug where the Windows SCM `/run` command was bypassing parts of the service control logic, preventing correct state reporting.
- **Executable-Relative Paths**: Enhanced `internal/config` to resolve relative paths (for database, repos, and logs) against the **executable's directory** rather than the current working directory. This ensures the service works correctly when its working directory is system-default (`C:\Windows\System32`).

## 3. Deployment & Logging Improvements
- **Unified Logging**: Refactored the internal logger to seamlessly integrate both standard Go `log` and `slog`.
- **Non-Interactive Compatibility**: Automatically detects non-interactive service sessions and skips `os.Stderr` writes to avoid initialization failures when standard streams are unavailable.

## 4. Test Suite Stabilization
The repository's test suite was fully synchronized with the updated interfaces and schema constants (`source_file` instead of `file`).
- **Restored Critical Tests**: Recovered original `config_test.go` and `batch_manager_test.go` test cases that were previously bypassed.
- **Schema Alignment**: Ensured all ingestion and search tests use the `Table:Repo_File` ID convention and modern schema constants.

---
*Refer to `walkthrough.md` for historical verification logs.*
