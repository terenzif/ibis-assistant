# Claude Desktop Integration Guide

This guide describes how to connect **Claude Desktop** to the **Ibis Assistant** using a local bridge.

## 🌉 Why a Bridge?

Claude Desktop currently supports "Stdio" (standard input/output) connections for local MCP servers. Ibis Assistant exposes HTTP/SSE so multiple MCP clients can share the same knowledge graph.

To connect them, we use a lightweight **MCP Bridge** that runs locally, talks "Stdio" to Claude, and forwards messages to the Ibis Assistant SSE endpoint.

---

## 🛠️ Installation & Setup

### 1. Prerequisite: The Ibis Assistant
Ensure the Ibis Assistant is running (e.g. `http://localhost:3030`).

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

## 📚 Available Tools Reference

The Ibis Assistant exposes the following tools to Claude:

### 🧠 Knowledge & Search
*   **`ask_project(query)`**: The primary entry point. Asks a natural language question about the codebase, history, or logic. It uses agentic reasoning to explore the graph.
*   **`optimize_knowledge(iterations)`**: Triggers a self-optimization loop (RAFT) to improve the server's retrieval accuracy.
*   **`reinforce_path(source, target, score)`**: Allows you to manually teach the AI that two items are related (or not).

### 📥 Ingestion (Admin/Setup)
*   **`ingest_git(path)`**: Indexes Git commit history.
*   **`ingest_code(path)`**: Vectorizes source code for semantic search.

### 🐞 Redmine Integration
*   **`redmine_search_issues(query)`**: Finds tickets by text.
*   **`redmine_get_issue(id)`**: Retrieves full details of a ticket.
*   **`redmine_update_issue(id, notes)`**: Adds comments/notes to a ticket.

---

## 💡 Usage Samples (Prompt Engineering)

Here are practical examples of how to leverage the system effectively.

### Scenario 1: Onboarding & Understanding
*For when you are new to a module or debugging legacy code.*

> **User**: "How does the authentication system work in this project? Explain the flow and show me the relevant files."
>
> **Claude**: Will use `ask_project` to retrieve the architecture, identify `auth.go` or `LoginController.cs`, and summarize the logic.

### Scenario 2: Historical Forensics
*For understanding why code is written a certain way.*

> **User**: "Why was the timeout increased in the database connection? Check the commit history."
>
> **Claude**: Will use `ask_project` (or explore via graph) to find the commit that changed the timeout, read its message (e.g., "Fixes #452"), and then fetch Redmine Issue #452 to reveal the root cause (e.g., "Production timeouts during nightly batch").

### Scenario 3: Task Execution (The "Senior Engineer" Loop)
*For planning and executing complex changes.*

> **User**: "I need to add a new field 'LoyaltyPoints' to the User model. Please analysis the impact."
>
> **Claude**:
> 1.  Uses `ask_project` to find the `User` struct/class.
> 2.  Identifies dependencies (DB schema, API DTOs, Frontend Types).
> 3.  Checks for related Redmine tickets (e.g., "Feature: Loyalty Program").
> 4.  Generates a step-by-step implementation plan warning you about specific files to touch.

### Scenario 4: Managing Work
*For updating project status without leaving the chat.*

> **User**: "Find the ticket about 'Slow Startup' and add a note that I've found the bottleneck in the logger initialization."
>
> **Claude**:
> 1.  Calls `redmine_search_issues("Slow Startup")` -> Returns Issue #905.
> 2.  Calls `redmine_update_issue("905", "Investigated: Root cause is synchronous logger init. Fixing now.")`.

### Scenario 5: Teaching the System
*For improving future answers.*

> **User**: "The relationship between `InvoiceService` and `TaxCalculator` is critical, but you missed it. Please reinforce that link."
>
> **Claude**: Calls `reinforce_path(source="file:InvoiceService.cs", target="file:TaxCalculator.cs", score=1.0)` to strengthen the association in the graph.
