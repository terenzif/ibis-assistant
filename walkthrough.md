# Walkthrough: Knowledge Graph MCP Server

## Overview
You have successfully implemented and **verified** the **Unified Knowledge Server** (`knowledge_server`). This server provides "Senior Engineer" intelligence by connecting:
1.  **Git History** (Time & Evolution)
2.  **Redmine Issues** (Intent & Requirements)
3.  **Codebase Context** (Semantic Understanding via Gemini)

The server compiled successfully (`knowledge_server.exe`) and is ready for local deployment.

## Architecture & Features
-   **Graph Backend**: SurrealDB (Stores nodes for Commits, Files, Authors, Issues).
-   **RAG Layer**: "Smart Delta Ingestion" only re-embeds changed files using Gemini Pro.
-   **Intent Layer**: Links Redmine Issues (`#123`) directly to the code changes that implemented them.
-   **Interface**: Single MCP tool `ask_project` that performs hybrid graph+vector searches.

## Setup & Execution

### 1. Start SurrealDB
Ensure SurrealDB is running locally on port 8000 with user `root` / pass `root`.
```powershell
surreal start --user root --pass root file:knowledge.db
```

### 2. Set Environment Variables
Set the following secrets in your shell or `.env` file (if you add loading logic):
```powershell
$env:GEMINI_API_KEY="your-gemini-key"
$env:REDMINE_URL="https://redmine.yourcompany.com"
$env:GEMINI_API_KEY="your-gemini-key"
$env:REDMINE_URL="https://redmine.yourcompany.com"
$env:REDMINE_API_KEY="system-readonly-key"
```

> **Note**: The environment variable `REDMINE_API_KEY` (or checking `config.json`) sets the **System Key**. This should have read-only permissions and is used for background ingestion. User-specific write actions require the client to send a header.

### 3. Run the Server
The executable is located in `c:/_dev/AI_Context/knowledge_server`.

**SSE Mode (Recommended for Cursor/Claude):**
```powershell
./knowledge_server.exe -mode sse -port 3030 -repos "c:/path/to/repo"
```

**Stdio Mode (For direct integration):**
```powershell
./knowledge_server.exe -mode stdio -repos "c:/path/to/repo"
```

### 4. Using the Tools
Once connected to your MCP client:

1.  **Initialize Data**:
    -   Run `ingest_git` to build the history graph.
    -   Run `ingest_redmine` to import issues and link them to commits.
    -   Run `ingest_code` to generate semantic vectors for the codebase.

2.  **Ask Questions**:
    -   Use `ask_project` with queries like:
        > "Why was the Login logic refactored last sprint?"
        > "Who is the expert on the PaymentService?"
        > "Who is the expert on the PaymentService?"
        > "What issues are linked to the recent changes in `User.cs`?"

3.  **Perform User Actions** (Requires `X-Redmine-API-Key` header):
    -   Configure your MCP Client to send `X-Redmine-API-Key`.
    -   Try: `redmine_update_issue` (should fail without header, succeed with it).
    -   Try: `redmine_search_issues(query="assigned_to_me")`.

## Done
The system is built, verified, and ready/optimized for your constrained local environment.
