<p align="center">
  <img src="docs/assets/logo.png" alt="Ibis Assistant" width="160" height="160">
</p>

# Ibis Assistant (MCP)

**Ibis Assistant** is an advanced MCP (Model Context Protocol) server that acts as a living project memory. It combines structural code analysis (Git), semantic understanding (vector RAG), and intent from ticketing (Redmine / Jira / Azure DevOps) into one queryable knowledge graph.

It gives AI agents (Claude, Copilot, Gemini, and others) context beyond the current working tree: history, evolution, and reasoning grounded in commits, issues, and code.

## Plug and play

Designed to get out of the way: **download a binary, configure once, run**.

1. Grab a release executable from [Releases](https://github.com/terenzif/ibis-assistant/releases) (no Go toolchain required for normal use).
2. On first launch with no `config.json`, an interactive **setup wizard** (`config fast`) probes hardware, configures hybrid AI, and writes keys into config (plug-and-play). Rerun with `ibis-assistant config`, `config full`, or open `http://127.0.0.1:<port>/settings/`.
3. SurrealDB, Ollama (embeddings **and** local chat), and related runtime pieces install or start **on demand** when enabled—aim is maximum automation, minimum manual wiring.
4. Start the server (`run` / `start` / Windows service) and connect your MCP client. Done.

Settings: browser UI at `http://127.0.0.1:<port>/settings/`, or `ibis-assistant config show`. Full guide: [docs/ai_and_settings.md](docs/ai_and_settings.md).

## Features

* **Plug-and-play onboarding**: Download an executable → wizard (first run or `config`) → auto-provision runtime deps → run.
* **Unified knowledge graph**: Files, commits, authors, branches, and issues in SurrealDB.
* **Git ingestion**: Builds causal links (`commit` → `changed` → `file`).
* **Local patch sync**: MCP `sync_local_patch` aligns **server clones** when `init_project` returns `requires_patch`. Personal/plugin live trees skip the patch (`status=live_tree`).
* **Multi-provider ticketing**: Links code changes to work items (`commit` → `implements` → `issue`).
* **Log analysis pipeline**: Recursive watch → LogAlign / known patterns / ast-grep first → AI residual → enrich (file/cause) → sidecar markdown report. See [docs/log_analysis_pipeline.md](docs/log_analysis_pipeline.md).
* **Email reporting**: Optional SMTP digests for anomalies and AI cost tracking.
* **Polyglot AST ingest**: ast-grep rules + `languageGlobs`; AI may synthesize missing YAML rules. See [docs/code_ingest_polyglot_and_rules.md](docs/code_ingest_polyglot_and_rules.md).
* **Efficient vector RAG**: Local Ollama embeddings by default (or Gemini); chat models are never used as the embedding space.
* **Hybrid AI reasoning**: Local Ollama chat (hardware auto-tier) + multi-cloud pool (Gemini, OpenAI-compatible, Claude). See [docs/ai_and_settings.md](docs/ai_and_settings.md).
* **Hybrid search**: Per-table vector queries + time decay + graph context via `ask_project`.
* **MCP-compliant**: Standard tools for plug-and-play MCP clients, including `settings_*`.

## Prerequisites

For **release binaries**, you mainly need **Git** on `PATH` and (for cloud reasoning) an AI key. SurrealDB and Ollama can be fetched/started automatically when enabled.

For **building from source**:

* **Go** 1.27+ (this repo pins `toolchain go1.27.1` in `go.mod`)
* **SurrealDB**: Managed automatically when possible (download on demand)
* **Ollama** (default local embeddings **and** local reasoning chat): auto-start / auto-install when enabled in config (AMD ROCm / NVIDIA CUDA via Ollama)
* **Git** on `PATH`
* **AI keys** (optional for local-only; enter via wizard/Settings UI, not ENV-first):
  * **Embedding**: Ollama by default (or Gemini)
  * **Reasoning**: hybrid default — Ollama local + cloud Gemini / OpenAI-compatible / Claude (`ai.reasoning.clouds`)

## Setup

### Recommended: download and run

Draft GitHub Releases with cross-platform binaries are published by CI when a `v*` tag is pushed. Download from [Releases](https://github.com/terenzif/ibis-assistant/releases), put the binary on your `PATH` (or run it from any folder), then:

```bash
ibis-assistant          # first run: config fast if config.json is missing
ibis-assistant config   # same as config fast
ibis-assistant config full
ibis-assistant run      # start the MCP server (Settings UI at /settings/)
```

Prefer that path over hand-editing config. **Never commit `config.json`** (it is gitignored). See [docs/ai_and_settings.md](docs/ai_and_settings.md).

### Build from source

```bash
git clone https://github.com/terenzif/ibis-assistant.git
cd ibis-assistant
go build -o ibis-assistant ./cmd/server   # on Windows: ibis-assistant.exe
```

You can still copy `config_master.json` to `config.json` and edit by hand if you prefer not to use the wizard.

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
      "provider": "hybrid",
      "model": "auto",
      "context_only_fallback": true,
      "clouds": {
        "gemini": {
          "keys": [
            { "key": "", "rpm": 15, "tpm": 30000, "rpd": 1500, "owner": "default" }
          ]
        },
        "openai_compat": {
          "base_url": "https://api.openai.com/v1",
          "api_key": "",
          "model": "gpt-4.1"
        },
        "claude": {
          "api_key": "",
          "model": "claude-sonnet-4"
        }
      },
      "routing": {
        "cloud_provider": "auto",
        "use_cloud_when_no_gpu": true
      }
    }
  },
  "db_auto_update": true,
  "auto_scan": true,
  "logs_root": "./logs"
}
```

Full AI field reference: [docs/ai_and_settings.md](docs/ai_and_settings.md). Tracked template: `config_master.json`.

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

With no arguments, the binary prints a short command guide. If `config.json` is missing, **`config fast`** runs first.

| Command | Purpose |
|---------|---------|
| `run` | Interactive server (MCP + Settings UI at `/settings/`) |
| `start` / `stop` | Background daemon (`server.log`, `ibis-assistant.pid`) |
| `config` / `config fast` | Fast setup (probe + hybrid AI + keys) |
| `config full` | Full setup (DB, SMTP, ticketing, …) |
| `config show` | Masked AI summary + settings URL |
| `config recommend` | JSON recommendations |
| `version` | Print the binary version (release builds embed the git tag) |
| `/install` `/uninstall` | Windows service (admin required) |

MCP settings tools: `settings_get`, `settings_apply`, `settings_open_ui`. Guide: [docs/ai_and_settings.md](docs/ai_and_settings.md).

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

* Browsers (`Accept: text/html`): install/usage guide (includes AI/Settings), endpoints, client snippets, and tool schemas
* Agents (`Accept: application/json`): Streamable HTTP URL, protocol versions (including 2026-07-28), `runtime_mode`, `settings_ui` / `settings_api`, auth headers, tools. After MCP connect, `server/discover` on `/mcp` is enough to attach.
* Markdown (`Accept: text/markdown`) and MCP resource `ibis://guide`: the same guide for a connected agent to show a human
* Settings UI: `http://localhost:3030/settings/` (when the HTTP listener is up)

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
* `docs/` — architecture and client guides (English). Start with [docs/naming.md](docs/naming.md), [docs/runtime_modes.md](docs/runtime_modes.md), [docs/ai_and_settings.md](docs/ai_and_settings.md), and [docs/changelog_20260919.md](docs/changelog_20260919.md).

## Roadmap vision

Ibis Assistant is meant to stay useful when the cloud is optional:

* **Local LLMs first-class** — Ollama embeddings and **hybrid local chat** (auto-tier) are in; deepen quality and AMD ROCm UX; cloud providers remain adapters in a pool.
* **Continuous learning** — close the loop from real use: reinforce useful graph paths, decay noise, learn from agent outcomes and feedback so retrieval and tooling improve over time instead of staying a static index.

**Walked on 18–19 Sep 2026** (this branch stack): three runtimes with **live working trees** for personal/plugin; Streamable HTTP `/mcp`; Cursor **stdio sidecar**; **hybrid AI + Settings UX** ([docs/ai_and_settings.md](docs/ai_and_settings.md)). See also [docs/runtime_modes.md](docs/runtime_modes.md).

**Still open:** closed learning loop from agent outcomes. Also still out of scope: OAuth, moving `mcp-bridge` off SSE, in-process Go in Cursor / `vscode.lm`.

Contributions that move those axes forward are especially welcome.

## Agent notes (for contributors working on this repo)

* Treat `cmd/server/main.go` as the MCP tool source of truth
* Prefer `ticket_*` over deprecated `redmine_*` names
* Keep `config_master.json` tracked; never commit local `config.json`
* Hybrid AI defaults: local Ollama auto-tier + cloud Gemini/OpenAI-compat/Claude; Settings UI at `/settings/`
* Design: `docs/superpowers/specs/2026-09-19-hybrid-reasoning-settings-design.md`
* Keep `AGENTS.md` local-only (gitignored); do not commit it
* AST chunking/logs go through the **ast-grep** sidecar (`sg`)
* Production layout is built into `dist/` via `make dist`

## License

MIT — see [LICENSE](LICENSE).

---

Ibis Assistant — Graph + RAG + Timeline context server.
