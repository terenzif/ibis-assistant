# Runtime modes, HTTP MCP, and the Cursor plugin

**Date:** 2026-09-18 (AI/Settings section updated 2026-09-19)  
**Status:** Implemented. Design: [superpowers/specs/2026-09-18-runtime-modes-design.md](superpowers/specs/2026-09-18-runtime-modes-design.md). Hybrid AI: [ai_and_settings.md](ai_and_settings.md).

Ibis Assistant is **one Go binary** (`ibis-assistant`) with three **runtimes** (`runtime_mode` in `config.json` / `RUNTIME_MODE` / `-runtime-mode`). Listener kind (`mode`: `sse` | `stdio`) is separate.

| Runtime | Process | Workspace | Knowledge store |
|---------|---------|-----------|-----------------|
| `personal` | `run` / daemon / Windows service on the same PC as the repos | Live working tree (`projects[].working_repo_path`, else `git_repos` by folder basename, else `-workspace` / `IBIS_WORKSPACE`) | Exe-relative / `config.json` SurrealDB |
| `server` | Same binary on a dedicated host | Owned clone at `discovery_root/dynamic/<name>` (`git clone` / `reset --hard` / `fetch` allowed) | Host-local SurrealDB |
| `plugin` | Cursor-owned **stdio child** (no Windows service for that session) | Opened folder (`IBIS_WORKSPACE` / Cursor workspace env / MCP roots / git cwd — never the user home tree) | Isolated dir: `%LOCALAPPDATA%\ibis-assistant\plugin` or `IBIS_DATA_DIR` |

Empty `runtime_mode` keeps the old all-interfaces HTTP bind and **owned clones** (backward compatible).

Personal/plugin **never** clone a missing path and **never** `git clean` / `reset --hard` on a developer tree. `sync_local_patch` on a live tree returns `{"status":"live_tree",...}` (dirty files are already visible). Server clones still apply the patch.

Plugin start:

- Forces `mode=stdio` unless `-mode` is set.
- Does not load a personal `config.json` from CWD or next to the exe (only `IBIS_DATA_DIR` / plugin data dir).
- Default Surreal URL becomes `ws://127.0.0.1:18000/rpc` so it does not attach to a personal DB on 8000.
- Warns on stderr/log if `ibis-assistant.pid` is running or the configured HTTP port is in use; continues on stdio and does **not** auto-attach.
- Does **not** put `${workspaceFolder}` in plugin `mcp.json` (Cursor empty windows fail variable resolve and mark the shared MCP id as error). Binds the opened folder from `IBIS_WORKSPACE`, Cursor/VS Code workspace env, MCP `roots/list`, or a git cwd that is not the user home directory. Does **not** AutoScan the process cwd (often the user home directory).
- An empty-window child **completes the MCP handshake** without SurrealDB or ingest so the identifier stays connected; it does not `os.Exit(0)`.
- Stdio: application logs go only to `plugin.log`. The Surreal Go driver is redirected off `stdout` so JSON slog cannot break the MCP JSON-RPC stream.
- AI: same hybrid stack as personal; keys live in the plugin data-dir `config.json`. Context-only fallback when local and cloud fail. MCP `settings_*` tools; open Settings via Cursor command or a personal HTTP `/settings/` when available. See [ai_and_settings.md](ai_and_settings.md).

## HTTP MCP (personal / server)

When `mode` is `sse` (HTTP listener):

- **Primary:** Streamable HTTP `POST /mcp` (mcp-go v1.1.0; protocol 2026-07-28 `server/discover` plus legacy `initialize`).
- **Legacy:** `/sse` + `/message` (deprecated; kept for `mcp-bridge`).
- **Discovery:** `GET /` with `Accept: application/json` | `text/html` | `text/markdown`; MCP resource `ibis://guide`.
- **Settings UI:** `GET /settings/`, `GET|POST /api/v1/settings` (localhost). Same app as Cursor `settings_open_ui`.
- CLI is a Streamable HTTP client of `/mcp` (`ibis-assistant ask|ingest|logs|…`). `POST /api/v1/cli/call` is gone.
- `init_project` **blocks** through git sync **and** ingest; MCP `notifications/progress` when the client sends `progressToken`.

Bind: personal/plugin → `127.0.0.1` unless `bind_address` is set; server → `0.0.0.0`; empty runtime → all interfaces (`:port`).

## Cursor plugin

Canonical source: [`cursor-plugin/`](../cursor-plugin/). Local copy: `~/.cursor/plugins/local/ibis-assistant/`.

Spawns `ibis-assistant -mode stdio -runtime-mode plugin` with `RUNTIME_MODE=plugin`. Put the Go binary on `PATH`. Do not zip the binary into the plugin. Do not interpolate `${workspaceFolder}` in the plugin spawn env.

Commands: **Ibis: Open Settings** (`cursor-plugin/commands/ibis-settings.md`).

## Config sketch (personal)

```json
{
  "runtime_mode": "personal",
  "projects": [
    { "name": "ibis-assistant", "working_repo_path": "C:/devsrc/ibis-assistant" }
  ]
}
```

Server `projects[]` entries may use `url` / `branch` instead of `working_repo_path`. Wizard: `ibis-assistant config fast` (default) or `config full`.

## Verification

`go test ./internal/e2erun ./internal/workspace ./internal/config ./internal/runtime ./internal/httpserver ./internal/cli ./internal/cursorplugin ./internal/ingest/dynamic ./internal/settings ./internal/ai`

Covered (18 Sep): live-tree ingest; server clone under `dynamic/`; plugin isolation; HTTP `/mcp`; plugin `mcp.json` spawn env.  
Covered (19 Sep): hybrid router + cloud pool unit tests; SettingsEngine tier/recommend; wizard package build.

## Still out of scope

OAuth, moving `mcp-bridge` off SSE, OS symlink/junction aliases, in-process Go in Cursor / `vscode.lm`, closed-loop continuous learning from agent outcomes.

Hybrid local **reasoning** and multi-cloud settings are documented in [ai_and_settings.md](ai_and_settings.md).
