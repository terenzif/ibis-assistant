# Implementation Plan - Knowledge Graph MCP Server

## [Goal] The "Universal Senior Engineer" Context Server
Build a **Graph + RAG + Timeline** knowledge server that gives *any* AI (Claude, Copilot, Gemini) deep, structural, and historical understanding of the codebase.
**Key Philosophy**:
-   **Graph**: Deterministic facts (Commits, Branches, File Dependencies).
-   **RAG**: Semantic content (Code meaning, Documentation).
-   **Timeline**: Evolutionary context (How X changed over time).
-   **Intent**: Work Items & Requirements (Redmine Issues -> Commits).

## User Review Required
> [!IMPORTANT]
> **Unified Interface**: The server will integrate `redmine_mcp` logic to link **Issues** directly to **Commits** in the graph (e.g. `Issue #123` -> `parent_of` -> `Commit A`).
> **Redmine Integration**: Shifted from "Polling" to "On-Demand". We only fetch tickets referenced in commits (e.g. `#123`) or manually requested, to avoid polluting the graph with irrelevant legacy tickets.
> **SurrealDB Suitability**: SurrealDB is the *perfect* fit here because it natively handles **Graph** (relationships) + **Document** (content) + **Vector** (search) in one engine.
> **Constraint Management**: To handle "Timeline Reasoning" on a small server, we will **pre-compute** costly graph traversals (like "churn heatmaps") during ingestion, rather than at query time.
> **Rate Limiting**: Configurable RPM for Gemini API is essential for the "RAG" part.

## Proposed Changes

### 1. Configuration & Abstraction [Refined]
-   **Config System**: Use `koanf` or `viper` (or simple standard lib) to load `config.yaml` with env var overrides (e.g. `KNOWLEDGE_RPM`).
    -   Default: Zero-conf startup (scans current dir, default local DB).
-   **AI Layer**:
    -   Interface `Provider` (allows future OpenAI/Claude).
    -   **Key Pooling**: Parse `GEMINI_API_KEY` as comma-separated list. Round-robin usage for `ingest_code`.


### 1. Project Restructuring
-   **Completed**: `knowledge_server` established.
-   **Completed**: Analyze `redmine_mcp` (Integrated into `internal/ingest/redmine`).
-   **Completed**: Redmine User-Specific Authentication (Privacy & Permissions).
-   **Scope**: Support **Multi-Repo** ingestion + **Redmine** integration.

### 2. Service Architecture (Go + SurrealDB)
**Library**: `mark3labs/mcp-go`.

#### [NEW] `knowledge_server/`
-   `cmd/server/`: Flags: `-mode sse`, `-repos "..."`.
-   `internal/schema/`: Defines the "Graph + Time + Intent" model.
    -   **Nodes**: `repo`, `branch`, `commit`, `author`, `file`, `issue` (Redmine), `tracker` (Epic/Feature).
    -   **Edges**:
        -   `contains` (Repo -> Branch, Repo -> File)
        -   `parent_of` (Commit -> Commit)
        -   `pointed_to` (Branch -> Commit)
        -   `changed` (Commit -> File) ~ *with diff stats*
        -   `authored` (Author -> Commit)
        -   `implements` (Commit -> Issue) ~ *Extracted from messages "#123"*
        -   `part_of` (Issue -> Tracker/Epic)

### 3. Feature: Deep Graph Ingestion (The "Structure")
Migrate and enhance `ingest_git.py` to Go.
-   **Logic**:
    1.  **Upsert Repos/Branches**.
    2.  **Graph Construction** (`parent_of`).
    3.  **Cross-Boundary Linking**: Parse commit messages for `#123` and link to `issue` nodes.

### 4. Feature: Redmine Ingestion (The "Intent")
**Strategy**: "On-Demand Ingestion".
-   **Ingestion**: `internal/ingest/redmine`.
-   **Logic**:
    1.  **Trigger**:
        -   **Git Ingestion**: When commit message has `#123`.
        -   **Manual MCP Tool**: `add_ticket_context(id)`.
    2.  **Fetch**: Query just that specific issue ID.
    3.  **Upsert**: Store `issue` node.
    4.  **Link**: Relate to commit.


### 5. Feature: Project Intelligence (The "Content")
**Strategy**: "Smart Delta RAG".
-   **Ingestion**: `internal/ingest/code`.
-   **Process**:
    1.  Compute file hash.
    2.  If changed, generate embeddings (Gemini).
    3.  Store as `file_chunk` connected to the `file` node.

### 6. Feature: Unified Interface (The "Endpoint")
Expose a single MCP toolset that queries the unified graph **AND** provides direct Redmine access.

#### Core Knowledge Tools
-   `ask_project(query)`: Unified RAG + Graph search.
-   `ingest_git(repos)`: Trigger analysis.
-   `ingest_code(repos)`: Trigger vectorization.

#### Direct Redmine Tools (Maintained from redmine-mcp)
-   `redmine_get_issue(id)`: Direct fetch.
-   `redmine_search_issues(query)`: Search tracker.
-   `redmine_update_issue(id, notes)`: Write back.
*Note: These allow the agent to interact with Redmine even if the Knowledge Graph is rebuilding or incomplete. **Requires `X-Redmine-API-Key` header for write actions.** *

## 7. Autonomous Agents (Imagination Analysis)
These agents run alongside the server to actively maintain and use the knowledge.

1.  **The "Fixer" (CI/CD Guardian)**
    -   **Trigger**: Build Failure (detected via log watcher).
    -   **Action**: Queries Knowledge Graph for "Recent changes in files mentioned in error stack trace".
    -   **Logic**: "Commit X changed `Login.cs`, causing `NullReference`. Revert or Apply Fix based on RAG of `Login.cs`."

2.  **The "Librarian" (Doc Maintainer)**
    -   **Trigger**: Commit to `docs/` or high-churn code areas.
    -   **Action**: Checks if `README.md` or Wiki pages are outdated compared to code logic (RAG comparison).
    -   **Logic**: "Function `Auth` signature changed, but `API.md` still shows old params. Generate Pull Request to update docs."

3.  **The "Squire" (Junior Dev Simulator)**
    -   **Trigger**: Pre-commit hook or Pull Request.
    -   **Action**: Compares new code against "Anti-patterns" stored in the Knowledge Graph (derived from historical revert commits).
    -   **Logic**: "You are introducing a pattern similar to Commit Y, which caused Bug Z. Warning: Check for race condition."

4.  **The "Archaeologist" (Legacy Explorer)**
    -   **Trigger**: Scheduled Weekly.
    -   **Action**: Scans for "Dead Code" (nodes with 0 incoming `calls` edges in a static analysis graph) or "Rotting Code" (uncoupled, old timestamp).
    -   **Logic**: Proposes refactoring tasks for Technical Debt reduction.

## Verification Plan

### Automated Tests
-   **Graph Integrity**: Test `Commit -> Issue` linking.
-   **Timeline Query**: Test a SurrealQL query that aggregates changes over time for a file.

### Manual Verification
1.  **Ingest**: Run on `DeckOnLine` + `DeckDb`.
2.  **Query Graph**: "Show me all branches touching `Login.cs`".
3.  **Query Hybrid**: "Explain the evolution of `PaymentService`" (Graph + RAG).
