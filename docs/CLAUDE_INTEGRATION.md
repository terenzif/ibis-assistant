# Claude Desktop Integration Guide

This guide describes how to connect **Claude Desktop** to the **Ibis Assistant** using a local bridge.

## 🌉 Why a Bridge?

Claude Desktop currently supports "Stdio" (standard input/output) connections for local MCP servers. Ibis Assistant's **product default** for HTTP clients is Streamable HTTP at `/mcp`. Legacy HTTP+SSE (`/sse`) remains so `mcp-bridge` and older clients can share the same knowledge graph.

To connect Claude Desktop, we use a lightweight **MCP Bridge** that runs locally, talks "Stdio" to Claude, and forwards messages to the Ibis Assistant **SSE** endpoint (`http://localhost:3030/sse`) until the bridge is moved onto `/mcp`.

---

## 🛠️ Installation & Setup

### 1. Prerequisite: The Ibis Assistant
Ensure the Ibis Assistant is running (e.g. `http://localhost:3030/mcp` for new HTTP clients; the bridge below still uses `/sse`).

### 2. Build the Bridge Tool
If you haven't already, compile the bridge tool included in this repository.

```powershell
# In the repository root
go build -o mcp-bridge.exe ./tools/mcp-bridge
```

You should now have `mcp-bridge.exe` in your folder. Move it to a permanent location if desired (e.g., `C:\Tools\mcp-bridge.exe`).

### 3. Configure Claude Desktop
Open your Claude Desktop configuration file:
*   **Windows**: `%APPDATA%\Claude\claude_desktop_config.json`
*   **Mac**: `~/Library/Application Support/Claude/claude_desktop_config.json`

Add the following entry to the `mcpServers` section:

```json
{
  "mcpServers": {
    "knowledge-graph": {
      "command": "C:\\Tools\\mcp-bridge.exe",
      "args": [
        "-url", "http://localhost:3030/sse",
        "-key", "YOUR_REDMINE_API_KEY"
      ]
    }
  }
}
```

> **Note**: Replace `YOUR_REDMINE_API_KEY` with your personal Redmine API Key found in your Redmine account page. This is required for actions like updating tickets.

Restart Claude Desktop. You should see a green connection icon for "knowledge-graph".

---

## Available tools reference

Source of truth: `cmd/server/main.go`. Obsolete names (`ingest_git`, `reinforce_path`) are **not** registered.

### Knowledge and search

* **`ask_project(query, branch_or_commit?)`**: Primary Q&A. Hybrid vector search (per-table + time decay) with graph context / agentic exploration.
* **`provide_collaborative_memory(project_name, memory_text, embedding?)`**: Add collaborative memory into the graph.
* **`save_reasoning_outcome(project_name, question, outcome_text, useful_sources)`**: Persist outcomes and reinforce useful paths (replaces any old “reinforce_path” idea).
* **`optimize_knowledge(iterations?)`**: RAFT-style self-optimization loop.
* **`analyze_blast_radius(...)`** / **`find_dead_code(repo_name)`**: Structural impact / dead-symbol helpers.

### Ingestion and git

* **`init_project(project_name, origin_url, branch, commit?)`**: Clone/sync and start ingestion. May return `credentials_required`, `requires_patch`, or `aligned`.
* **`sync_local_patch(project_name, patch, commit?)`**: Apply a unified diff when the tip is not on the remote (`requires_patch`) **on a server-owned clone**. Personal/plugin live trees return `live_tree` instead.
* **`update_project_status(project_name, origin_url, branch, commit)`**: Queue status/commit updates.
* **`ingest_code`**: Vectorize / AST-chunk source (ast-grep + rules).
* **`git_configure_credentials(...)`**: Persist Git auth in SurrealDB.
* **`analyze_logs(project_name, log_text, log_file?)`**: On-demand log analysis (static → AI → enrich). See [log_analysis_pipeline.md](log_analysis_pipeline.md).

### Ticketing (`ticket_*`) and PRs (`repo_pr_*`)

Full list and playbooks: [TICKETING_CLIENT.md](TICKETING_CLIENT.md) and [../copilot-instructions.md](../copilot-instructions.md).

---

## Usage samples (prompt engineering)

Here are practical examples of how to leverage the system effectively.

### Scenario 1: Onboarding and understanding
*For when you are new to a module or debugging legacy code.*

> **User**: "How does the authentication system work in this project? Explain the flow and show me the relevant files."
>
> **Claude**: Uses `ask_project` to retrieve architecture, identify key files, and summarize the logic.

### Scenario 2: Historical forensics
*For understanding why code is written a certain way.*

> **User**: "Why was the timeout increased in the database connection? Check the commit history."
>
> **Claude**: Uses `ask_project` to find the commit and related issue context.

### Scenario 3: Local unpushed commit
*When `init_project` returns `requires_patch`.*

> **User**: "Index my local tip; it is not on the remote yet."
>
> **Claude**: Calls `init_project`, then `sync_local_patch` with a unified diff of the unpushed commits.

### Scenario 4: Log triage
*For production or local failures.*

> **User**: "Analyze this error log and tell me which file is at fault."
>
> **Claude**: Calls `analyze_logs` with the log text (and optional file name), then explains enrich results.

### Scenario 5: Task execution
*For planning complex changes.*

> **User**: "I need to add a new field 'LoyaltyPoints' to the User model. Please analyze the impact."
>
> **Claude**: Uses `ask_project` and optionally `analyze_blast_radius`, then proposes a plan.

### Scenario 6: Managing work
*For updating project status without leaving the chat.*

> **User**: "Find the ticket about 'Slow Startup' and add a note that I've found the bottleneck in the logger initialization."
>
> **Claude**: `ticket_search` → `ticket_update` / `ticket_add_comment`.

### Scenario 7: Teaching the system
*For improving future answers.*

> **User**: "Remember that InvoiceService and TaxCalculator are tightly coupled for tax calculation."
>
> **Claude**: Uses `provide_collaborative_memory` and/or `save_reasoning_outcome` with the useful sources.
