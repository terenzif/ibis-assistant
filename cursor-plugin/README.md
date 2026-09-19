<p align="center">
  <img src="assets/logo.png" alt="Ibis Assistant" width="128" height="128">
</p>

# Ibis Assistant for Cursor

Thin Cursor plugin that **spawns** the Ibis Assistant Go binary as an MCP stdio child. It does not embed Go in-process and does not ship the binary.

The opened folder is the live workspace (`RUNTIME_MODE=plugin`). Knowledge is stored under `%LOCALAPPDATA%\ibis-assistant\plugin` (or `IBIS_DATA_DIR`), separate from a personal `ibis-assistant` daemon. A Cursor empty window (no folder) still completes the MCP handshake (no `${workspaceFolder}` in `mcp.json`, no SurrealDB) so the shared plugin id is not marked failed. The opened folder is bound from `IBIS_WORKSPACE` / Cursor workspace env / MCP `roots/list` when present.

## Settings

- Command **Ibis: Open Settings** (`commands/ibis-settings.md`) → MCP `settings_open_ui`
- Tools: `settings_get`, `settings_apply`, `settings_open_ui`
- Same browser UI as personal mode: `http://127.0.0.1:<port>/settings/` when the HTTP listener is up

## Install

1. Build or install the Go binary and put it on `PATH` as `ibis-assistant` (Windows: `ibis-assistant.exe`).

   From the repo root, install into the user Go bin (already on a typical Windows PATH):

   ```powershell
   go build -o "$env:USERPROFILE\go\bin\ibis-assistant.exe" ./cmd/server
   ```

   A personal install (`ibis-assistant /install`) also works if that directory is on `PATH`.

   Cursor spawns the command **without** the repo as cwd. If the exe exists only in the working tree, MCP fails with: `ibis-assistant is not recognized as an internal or external command`.

2. Enable this plugin from the repo copy, or from the local Cursor plugins folder:

   - Canonical source: `cursor-plugin/` in [ibis-assistant](https://github.com/terenzif/ibis-assistant)
   - Local install: `~/.cursor/plugins/local/ibis-assistant/` (copied from `cursor-plugin/`)

3. Reload Cursor. The MCP server `ibis-assistant` starts with:

   ```text
   ibis-assistant -mode stdio -runtime-mode plugin
   RUNTIME_MODE=plugin
   ```

   Do **not** put `${workspaceFolder}` in plugin `mcp.json`. Cursor empty windows cannot resolve that variable and abort spawn, which marks the shared MCP identifier as error. The Go child binds the opened folder from `IBIS_WORKSPACE` (if you set it), Cursor/VS Code workspace env vars, MCP `roots/list`, or a git cwd that is not the user home directory.

## Personal daemon vs plugin

You can run a **personal** HTTP daemon (`ibis-assistant run` / `start` / Windows service) and this plugin at the same time. The plugin **does not attach** to that HTTP listener. It uses stdio and its own SurrealDB data directory.

If a personal process is already running (`ibis-assistant.pid`) or the default HTTP port is in use, the plugin logs a warning and continues on stdio.

Do not point a personal `db_data_path` at the plugin data dir, and do not run two plugin children against the same isolated DB if you can avoid it.

## Usage

In a Git checkout, call `init_project` (or ask Ibis Assistant about the repo). Personal/plugin runtimes read the working tree in place; they do not `git clone` or `git reset --hard`.

Optional env:

| Variable | Purpose |
|----------|---------|
| `IBIS_WORKSPACE` | Optional opened-folder path. Plugin `mcp.json` does not set `${workspaceFolder}` (empty windows cannot resolve it). |
| `IBIS_DATA_DIR` | Override plugin data directory |
| `RUNTIME_MODE` | Must stay `plugin` for isolation |

## Components

- `mcpServers` only (`mcp.json`)

The Go server, HTTP MCP (`/mcp`), and Windows service stay in the main repository, not in this plugin package.
