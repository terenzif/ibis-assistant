# Ibis Assistant (MCP)

**Ibis Assistant** is an advanced MCP (Model Context Protocol) server that acts as a living project memory. It combines structural code analysis (Git), semantic understanding (vector RAG), and intent from ticketing (Redmine / Jira / Azure DevOps) into one queryable knowledge graph.

It gives AI agents (Claude, Copilot, Gemini, and others) context beyond the current working tree: history, evolution, and reasoning grounded in commits, issues, and code.

## Features

* **Unified knowledge graph**: Files, commits, authors, branches, and issues in SurrealDB.
* **Git ingestion**: Builds causal links (`commit` → `changed` → `file`).
* **Multi-provider ticketing**: Links code changes to work items (`commit` → `implements` → `issue`).
* **Log analysis**: Ingests logs, classifies error patterns, and persists read offsets.
* **Email reporting**: Optional SMTP reports for anomalies and AI cost tracking.
* **Efficient vector RAG**: Gemini batch embeddings (or local Ollama) for large chunk sets.
* **Hybrid search**: Graph structure + semantic vectors.
* **MCP-compliant**: Standard tools for plug-and-play MCP clients.

## Prerequisites

* **Go** 1.22+ (toolchain pin may require Go 1.26 — see repo docs if you hit module errors)
* **SurrealDB**: Managed automatically when possible (download on demand)
* **Ollama** (default local embeddings): auto-start / auto-install when enabled in config
* **Git** on `PATH`
* **AI keys**:
  * **Embedding**: Ollama by default (or Gemini)
  * **Reasoning**: Gemini multi-key failover via `ai.reasoning.keys`

## Setup

```bash
git clone https://github.com/terenzif/ibis-server.git
cd ibis-assistant
go build -o ibis-assistant ./cmd/server   # on Windows: ibis-assistant.exe
```

Copy `config_master.json` to `config.json` and fill in your settings. **Never commit `config.json`** (it is gitignored).

```json
{
  "port": 3333,
  "mode": "sse",
  "db_url": "ws://127.0.0.1:8000/rpc",
  "db_user": "root",
  "db_password": "root",
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

### CLI (MCP over HTTP)

The same binary talks to a running server via `/api/v1/cli/call`:

```bash
./ibis-assistant ask "Explain the auth flow" --branch main
./ibis-assistant ingest code --path ./path/to/project
./ibis-assistant ingest git --name MyApp --url https://github.com/org/repo --branch main
./ibis-assistant ticket search --query "login bug" --provider jira
./ibis-assistant pr create --source feature/x --target main --title "Add feature"
./ibis-assistant credentials add --target github.com --token MY_PAT
```

### HTTP auto-discovery (`GET /`)

Example: `http://localhost:3030/`

* Browsers (`Accept: text/html`): dark dashboard with endpoints, client snippets, and tool schemas
* Programmatic clients (`Accept: application/json`): structured tool metadata for auto-configuration

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

## MCP clients

Default SSE endpoint: `http://localhost:3030/sse`

### Claude Desktop

See **[docs/CLAUDE_INTEGRATION.md](docs/CLAUDE_INTEGRATION.md)**. Build `tools/mcp-bridge` and point it at the SSE URL.

### VS Code / other MCP clients

```json
"mcpServers": {
  "ibis-assistant": {
    "url": "http://localhost:3030/sse",
    "transport": "sse",
    "headers": {
      "X-Redmine-API-Key": "YOUR_USER_KEY"
    }
  }
}
```

Ticketing identity headers (optional overrides):

* `X-Redmine-API-Key`
* `X-Jira-Email` / `X-Jira-API-Token`
* `X-Azure-DevOps-PAT`

### MCP Inspector

```bash
npx @modelcontextprotocol/inspector http://localhost:3030/sse
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
* `internal/` — schema, ingest (git/code/logs/redmine), db, search, ticketing, AI
* `rules/` — ast-grep YAML extractors
* `docs/` — architecture and client guides (primary docs are English; some historical design notes may still be Italian)

## Agent notes (for contributors working on this repo)

* Treat `cmd/server/main.go` as the MCP tool source of truth
* Prefer `ticket_*` over deprecated `redmine_*` names
* Keep `config_master.json` tracked; never commit local `config.json`
* AST chunking/logs go through the **ast-grep** sidecar (`sg`)
* Production layout is built into `dist/` via `make dist`

## License

MIT — see [LICENSE](LICENSE).

---

Ibis Assistant — Graph + RAG + Timeline context server.
