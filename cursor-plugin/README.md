# Ibis Assistant for Cursor

Thin Cursor plugin that **spawns** the Ibis Assistant Go binary as an MCP stdio child. It does not embed Go in-process and does not ship the binary.

The opened folder is the live workspace (`RUNTIME_MODE=plugin`). Knowledge is stored under `%LOCALAPPDATA%\ibis-assistant\plugin` (or `IBIS_DATA_DIR`), separate from a personal `ibis-assistant` daemon.

## Install

1. Build or install the Go binary and put it on `PATH` as `ibis-assistant` (Windows: `ibis-assistant.exe`):

   ```bash
   go build -o ibis-assistant.exe ./cmd/server
   ```

   A personal install (`ibis-assistant /install` or copying the exe into a directory on `PATH`) also works.

2. Enable this plugin from the repo copy, or from the local Cursor plugins folder:

   - Canonical source: `cursor-plugin/` in [ibis-assistant](https://github.com/terenzif/ibis-assistant)
   - Local install: `~/.cursor/plugins/local/ibis-assistant/` (copied from `cursor-plugin/`)

3. Reload Cursor. The MCP server `ibis-assistant` starts with:

   ```text
   ibis-assistant -mode stdio
   RUNTIME_MODE=plugin
   ```

## Personal daemon vs plugin

You can run a **personal** HTTP daemon (`ibis-assistant run` / `start` / Windows service) and this plugin at the same time. The plugin **does not attach** to that HTTP listener. It uses stdio and its own SurrealDB data directory.

If a personal process is already running (`ibis-assistant.pid`) or the default HTTP port is in use, the plugin logs a warning and continues on stdio.

Do not point a personal `db_data_path` at the plugin data dir, and do not run two plugin children against the same isolated DB if you can avoid it.

## Usage

In a Git checkout, call `init_project` (or ask Ibis Assistant about the repo). Personal/plugin runtimes read the working tree in place; they do not `git clone` or `git reset --hard`.

Optional env:

| Variable | Purpose |
|----------|---------|
| `IBIS_WORKSPACE` | Override the opened folder / git root |
| `IBIS_DATA_DIR` | Override plugin data directory |
| `RUNTIME_MODE` | Must stay `plugin` for isolation |

## Components

- `mcpServers` only (`mcp.json`)

The Go server, HTTP MCP (`/mcp`), and Windows service stay in the main repository, not in this plugin package.
