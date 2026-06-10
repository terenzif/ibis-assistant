# Implementation Plan: Ibis Arc Hybrid Search
**Target Developer:** Jules
**Status:** Ingestion is COMPLETE. Retrieval is PENDING.
**Objective:** Implement "AskProject" functionality combining Vector Search with Graph Traversal in `ibis-arc`.

## 1. Confirmed Codebase State
-   **Ingestion (`internal/ingest/`)**:
    -   `git`: Already populates `commit` nodes and links them to `file` (`changed`) and `issue` (`implements`).
    -   `code`: Already populates `file_chunk` with embeddings.
    -   `redmine`: Already populates `issue` details.
-   **Service (`cmd/server/main.go`)**:
    -   Fully wired with `auth`, `config`, and `background indexing`.
    -   `AskProject` tool is registered but calls a stub in `search.go`.
-   **Search (`internal/search/search.go`)**:
    -   **STUB**: Currently performs a vector search but returns empty results.
    -   Missing the logic to traverse from `file_chunk` back to `commit` and `issue`.

## 2. Architecture
The logic resides in `internal/search/`.
-   **`search.go`**: High-level orchestration.
-   **`graph.go`** (New): Graph query construction and parsing.

### Data Model (Verified in `internal/schema/schema.go`)
-   **Nodes**: `file_chunk`, `file`, `commit`, `issue`, `author`.
-   **Edges**: 
    -   `commit` -> `changed` -> `file`
    -   `commit` -> `implements` -> `issue`
    -   `author` -> `authored` -> `commit`

## 3. Implementation Steps

### Step 1: Create `internal/search/graph.go`
Create a helper to encapsulate SurrealQL graph queries.

```go
package search

// GraphContext represents the historical context of a file
type GraphContext struct {
    FileID      string
    Commits     []CommitSummary
    Issues      []IssueSummary
}

type CommitSummary struct {
    Hash    string
    Message string
    Author  string
    Date    string
}

type IssueSummary struct {
    ID      string
    Subject string
    Status  string
}

// GetFileContext gives us the "Story" of a file from the graph
// Query logic:
// SELECT * FROM (
//    SELECT 
//       hash, message, date, 
//       <-authored.name as author,
//       ->implements->issue.{id, subject, status} as issues
//    FROM commit 
//    WHERE ->changed->file.path = $filePath
//    ORDER BY date DESC 
//    LIMIT 5
// )
```

### Step 2: Update `internal/search/search.go`
Implement the `AskProject` function.

#### Algorithm:
1.  **Embed Query**: Use `s.AI.EmbedText(query)`.
2.  **Vector Search**:
    ```sql
    SELECT 
        id, 
        content, 
        file_path, -- ensure this is stored on chunk or join
        vector::similarity::cosine(embedding, $vec) as score 
    FROM file_chunk 
    ORDER BY score DESC 
    LIMIT 5;
    ```
3.  **Graph Enrichment**:
    For each unique `file_path` found:
    -   Call `GetFileContext(path)`.
    -   Collect recent commits and linked issues.
4.  **Synthesis (LLM or Structured)**:
    -   For now, strictly formatted text response is fine.
    -   Format:
        > **Found in [AuthService.cs] (Score: 0.89)**
        > *...code snippet...*
        >
        > **Context**:
        > - Modified by **Jules** on 2024-01-20: "Fix login bug" (Linked to Issue #123)
        > - Modified by **Admin** on 2024-01-15: "Init feature"

### Step 3: Verify Integration in `cmd/server/main.go`
-   The tool `ask_project` is already registered.
-   Ensure the returned `CallToolResultText` properly displays the rich string returned by `AskProject`.

## 4. Verification
Run the server and test with `ask_project("What recent changes were made to the search logic?")`.
**Success Criteria**:
-   Response contains code snippets.
-   Response contains **Commit Messages** and **Dates**.
-   Response contains **Issue IDs** if linked.
