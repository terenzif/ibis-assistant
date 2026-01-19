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
> **SurrealDB Suitability**: SurrealDB is the *perfect* fit here because it natively handles **Graph** (relationships) + **Document** (content) + **Vector** (search) in one engine.
> **Constraint Management**: To handle "Timeline Reasoning" on a small server, we will **pre-compute** costly graph traversals (like "churn heatmaps") during ingestion, rather than at query time.
> **Rate Limiting**: Configurable RPM for Gemini API is essential for the "RAG" part.

## Proposed Changes

### 1. Project Restructuring
-   **Completed**: `knowledge_server` established.
-   **Pending**: Analyze `redmine_mcp` (user will checkout).
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
**Source**: Ported from `redmine-mcp`.
-   **Ingestion**: `internal/ingest/redmine`.
-   **Logic**:
    1.  Poll `/issues.json?status_id=*&sort=updated_on:desc`.
    2.  Upsert `issue` nodes (ID, Subject, Status, Author).
    3.  Upsert `journal` entries as comment nodes or properties.
    4.  **Linking**:
        -   Commit Message `#123` -> `RELATE commit->implements->issue`.

### 5. Feature: Project Intelligence (The "Content")
**Strategy**: "Smart Delta RAG".
-   **Ingestion**: `internal/ingest/code`.
-   **Process**:
    1.  Compute file hash.
    2.  If changed, generate embeddings (Gemini).
    3.  Store as `file_chunk` connected to the `file` node.

### 6. Feature: Unified Interface (The "Endpoint")
Expose a single MCP toolset that queries the unified graph.
-   `ask_project("Why was the Login changed?")` ->
    1.  Find `Login.cs` changes (Graph).
    2.  Find linked Issues `#55` "Fix Login Bug" (Graph/Redmine).
    3.  Retrieve Issue description + Commit Diff.
    4.  Synthesize answer.

## Verification Plan

### Automated Tests
-   **Graph Integrity**: Test `Commit -> Issue` linking.
-   **Timeline Query**: Test a SurrealQL query that aggregates changes over time for a file.

### Manual Verification
1.  **Ingest**: Run on `DeckOnLine` + `DeckDb`.
2.  **Query Graph**: "Show me all branches touching `Login.cs`".
3.  **Query Hybrid**: "Explain the evolution of `PaymentService`" (Graph + RAG).
