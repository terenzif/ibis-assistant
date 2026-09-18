<p align="center">
  <img src="docs/assets/logo.png" alt="Ibis Assistant" width="160" height="160">
</p>

# Ibis Assistant (MCP)

**Ibis Assistant** is an advanced MCP (Model Context Protocol) server that acts as a living project memory. It combines structural code analysis (Git), semantic understanding (vector RAG), and intent from ticketing (Redmine / Jira / Azure DevOps) into one queryable knowledge graph.

It gives AI agents (Claude, Copilot, Gemini, and others) context beyond the current working tree: history, evolution, and reasoning grounded in commits, issues, and code.

## Features

* **Unified knowledge graph**: Files, commits, authors, branches, and issues in SurrealDB.
* **Git ingestion**: Builds causal links (`commit` → `changed` → `file`).
* **Local patch sync**: MCP `sync_local_patch` aligns **server clones** when `init_project` returns `requires_patch`. Personal/plugin live trees skip the patch (`status=live_tree`).
* **Multi-provider ticketing**: Links code changes to work items (`commit` → `implements` → `issue`).
* **Log analysis pipeline**: Recursive watch → LogAlign / known patterns / ast-grep first → AI residual → enrich (file/cause) → sidecar markdown report. See [docs/log_analysis_pipeline.md](docs/log_analysis_pipeline.md).
* **Email reporting**: Optional SMTP digests for anomalies and AI cost tracking.
* **Polyglot AST ingest**: ast-grep rules + `languageGlobs`; AI may synthesize missing YAML rules. See [docs/code_ingest_polyglot_and_rules.md](docs/code_ingest_polyglot_and_rules.md).
* **Efficient vector RAG**: Gemini batch embeddings (or local Ollama) for large chunk sets.
* **Hybrid search**: Per-table vector queries + time decay + graph context via `ask_project`.
* **MCP-compliant**: Standard tools for plug-and-play MCP clients.

## Prerequisites

* **Go** 1.27+ (this repo pins `toolchain go1.27.1` in `go.mod`)
* **SurrealDB**: Managed automatically when possible (download on demand)
* **Ollama** (default local embeddings): auto-start / auto-install when enabled in config
* **Git** on `PATH`
* **AI keys**:
  * **Embedding**: Ollama by default (or Gemini)
  * **Reasoning**: Gemini multi-key failover via `ai.reasoning.keys`

## Setup

```bash
git clone https://github.com/terenzif/ibis-assistant.git
cd ibis-assistant
go build -o ibis-assistant ./cmd/server   # on Windows: ibis-assistant.exe
```

Copy `config_master.json` to `config.json` and fill in your settings. **Never commit `config.json`** (it is gitignored).

`runtime_mode` is `personal` (same PC as the repos, binds `127.0.0.1`), `server` (dedicated host, binds `0.0.0.0`), or `plugin` (Cursor stdio child). Leave it empty to keep the old all-interfaces bind. Optional `bind_address` overrides the host. See [docs/superpowers/specs/2026-09-18-runtime-modes-design.md](docs/superpowers/specs/2026-09-18-runtime-modes-design.md).

Personal and plugin modes ingest the live working tree (no `git clone` / `reset --hard`). Point each project at its checkout with `projects[]`. `git_repos` remains a fallback list of absolute paths (matched by folder basename). Server mode (or empty `runtime_mode`) still clones under `discovery_root/dynamic/<name>`. Plugin data (SurrealDB, logs, optional `config.json`) lives under `%LOCALAPPDATA%\ibis-assistant\plugin` or `IBIS_DATA_DIR`, not next to a personal install.

```json
{
  "port": 3333,
  "runtime_mode": "personal",
  "mode": "sse",
  "db_url": "ws://127.0.0.1:8000/rpc",
  "db_user": "root",
  "db_password": "root",
  "projects": [
    { "name": "ibis-assistant", "working_repo_path": "C:/devsrc/ibis-assistant" }
  ],
  "ai": {
    "embedding": {
      "provider": "ollama",
      "model": "nomic-embed-text",
      "url": "http://127.0.0.1:11434",
      "auto_start": true,
      "auto_update": true
    },
    "reasoning": {
      "provider": "gemini",
      "model": "gemini-2.5-pro",
      "keys": [
        { "key": "", "rpm": 15, "tpm": 30000, "rpd": 1500, "owner": "default" }
      ]
    }
  },
  "db_auto_update": true,
  "auto_scan": true,
  "logs_root": "./logs"
}
```

Provider-aware ticketing lives in `config/ticketing_config.json` (see `config/ticketing_config.example.json`). Per-request credential overrides use HTTP headers.

### Ticketing + PR automation

Full client guide: **[docs/TICKETING_CLIENT.md](docs/TICKETING_CLIENT.md)**

* MCP tools `ticket_*` (search / get / create / update / workflow)
* MCP tools `repo_pr_*` (Azure DevOps PR create / complete)
* Runtime header overrides for provider credentials

## Run

```bash
./ibis-assistant run -port 3030 -mode sse
# or
./ibis-assistant /run
```

With no arguments, the binary prints a short command guide. If `config.json` is missing, an interactive config wizard starts first.

| Command | Purpose |
|---------|---------|
| `run` | Interactive server (MCP) |
| `start` / `stop` | Background daemon (`server.log`, `ibis-assistant.pid`) |
| `config` | Interactive config wizard |
| `/install` `/uninstall` | Windows service (admin required) |

### CLI (MCP over Streamable HTTP)

The same binary is an MCP client of a running server (`POST /mcp`, JSON-RPC `tools/call`, progress notifications):

```bash
./ibis-assistant ask "Explain the auth flow" --branch main
./ibis-assistant ingest code --path ./path/to/project
./ibis-assistant ingest git --name MyApp --url https://github.com/org/repo --branch main
./ibis-assistant logs analyze --project MyApp --text "ERROR timeout talking to db"
./ibis-assistant ticket search --query "login bug" --provider jira
./ibis-assistant pr create --source feature/x --target main --title "Add feature"
./ibis-assistant credentials add --target github.com --token MY_PAT
./ibis-assistant memory --help          # collaborative memory helpers
./ibis-assistant outcome --help         # save_reasoning_outcome
./ibis-assistant optimize --help        # optimize_knowledge
```

MCP-only (no CLI wrapper yet): `sync_local_patch`, `analyze_blast_radius`, `find_dead_code`, `update_project_status`.

### HTTP auto-discovery (`GET /`)

Example: `http://localhost:3030/`

* Browsers (`Accept: text/html`): install/usage guide, endpoints, client snippets, and tool schemas
* Agents (`Accept: application/json`): Streamable HTTP URL, protocol versions (including 2026-07-28), `runtime_mode`, auth headers, tools. After MCP connect, `server/discover` on `/mcp` is enough to attach.
* Markdown (`Accept: text/markdown`) and MCP resource `ibis://guide`: the same guide for a connected agent to show a human

### Config precedence

1. CLI flags (e.g. `-port 9000`)
2. Environment variables (e.g. `PORT=9000`)
3. `config.json`
4. Built-in defaults

SurrealDB auto-update can be disabled with `"db_auto_update": false` or `DB_AUTO_UPDATE=false`.

### Git credentials

Priority:

1. Runtime headers `X-Git-Token` / `X-Git-PAT` (not stored)
2. SurrealDB `git_credential` store (via `git_configure_credentials`)
3. Legacy `git_tokens` in `config.json`
4. `GIT_TOKEN` environment variable

If `init_project` hits a private repo with no credentials, the server returns `{"status":"credentials_required", ...}` so the client can prompt for a PAT.

If the requested commit is not on the remote, a **server** may return `{"status":"requires_patch", "closest_known_commit":"…"}`. Clients should send a unified diff with MCP `sync_local_patch`. On a personal/plugin live tree that tool returns `live_tree` instead (see [copilot-instructions.md](copilot-instructions.md) and [docs/runtime_modes.md](docs/runtime_modes.md)).

## MCP clients

Default Streamable HTTP endpoint: `http://localhost:3030/mcp` (mcp-go v1.1.0; protocol **2026-07-28** `server/discover` plus legacy `initialize`).  
Legacy SSE (kept for `mcp-bridge`): `http://localhost:3030/sse`

### Claude Desktop

See **[docs/CLAUDE_INTEGRATION.md](docs/CLAUDE_INTEGRATION.md)**. Build `tools/mcp-bridge` and point it at the SSE URL.

### VS Code / other MCP clients

```json
"mcpServers": {
  "ibis-assistant": {
    "url": "http://localhost:3030/mcp",
    "headers": {
      "X-Redmine-API-Key": "YOUR_USER_KEY"
    }
  }
}
```

### Cursor plugin (stdio child)

Canonical package: [`cursor-plugin/`](cursor-plugin/). Copy it to `~/.cursor/plugins/local/ibis-assistant/` so Cursor can load it immediately.

The plugin runs `ibis-assistant -mode stdio -runtime-mode plugin` with `RUNTIME_MODE=plugin` (no `${workspaceFolder}` interpolation). Put the Go binary on `PATH` (not only in the repo folder):

```powershell
go build -o "$env:USERPROFILE\go\bin\ibis-assistant.exe" ./cmd/server
```

See [`cursor-plugin/README.md`](cursor-plugin/README.md).

Ticketing identity headers (optional overrides):

* `X-Redmine-API-Key`
* `X-Jira-Email` / `X-Jira-API-Token`
* `X-Azure-DevOps-PAT`

### MCP Inspector

```bash
npx @modelcontextprotocol/inspector http://localhost:3030/mcp
```

## Example questions

* “What does module X do, and how did it evolve?”
* “Why did `CalculateDiscount` change last week?”
* “If I change table `Prices`, which files are affected?”
* “Is there already logic for sending signed email?”

## Tests

```bash
go test ./...
```

## Layout

* `cmd/` — application entrypoints
* `internal/` — schema, ingest (git/code/logs/dynamic), db, search, ticketing, AI, workspace resolver
* `cursor-plugin/` — Cursor marketplace plugin (MCP stdio spawn; binary stays on PATH)
* `rules/` — ast-grep YAML extractors (hand-written + optional `ai-generated-*`)
* `sgconfig.yml` — ast-grep config including `languageGlobs`
* `docs/` — architecture and client guides (English). Start with [docs/naming.md](docs/naming.md), [docs/runtime_modes.md](docs/runtime_modes.md), and [docs/changelog_20260918.md](docs/changelog_20260918.md).

## Roadmap vision

Ibis Assistant is meant to stay useful when the cloud is optional:

* **Local LLMs first-class** — deepen Ollama (and similar) paths for embeddings *and* reasoning, so a full self-hosted loop works without mandatory cloud APIs; cloud providers remain adapters, not the core.
* **Continuous learning** — close the loop from real use: reinforce useful graph paths, decay noise, learn from agent outcomes and feedback so retrieval and tooling improve over time instead of staying a static index.

**Walked on 18 Sep 2026** (this branch stack, not yet master): three runtimes with **live working trees** for personal/plugin (no OS links, no `reset --hard` on developer checkouts); Streamable HTTP `/mcp` as the default MCP transport; dual discovery (agent JSON + human guide); blocking `init_project` with progress; Cursor **stdio sidecar** with isolated plugin data. See [docs/runtime_modes.md](docs/runtime_modes.md).

**Still open on those two axes:** local **reasoning** (embeddings can already be Ollama); a closed learning loop from agent outcomes. Also still out of scope: OAuth, moving `mcp-bridge` off SSE, in-process Go in Cursor.

Contributions that move those two axes forward are especially welcome.

## Agent notes (for contributors working on this repo)

* Treat `cmd/server/main.go` as the MCP tool source of truth
* Prefer `ticket_*` over deprecated `redmine_*` names
* Keep `config_master.json` tracked; never commit local `config.json`
* Keep `AGENTS.md` local-only (gitignored); do not commit it
* AST chunking/logs go through the **ast-grep** sidecar (`sg`)
* Production layout is built into `dist/` via `make dist`

## License

MIT — see [LICENSE](LICENSE).

---

Ibis Assistant — Graph + RAG + Timeline context server.
