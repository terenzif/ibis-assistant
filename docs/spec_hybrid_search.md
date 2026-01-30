# Implementation Plan: Knowledge Server Hybrid Search
**Target Developer:** Jules
**Objective:** Implement "AskProject" functionality combining Vector Search with Graph Traversal in `knowledge-server`.

## 1. Context & Goal
The `knowledge-server` currently ingests Git history and Codebase embeddings into SurrealDB. The `AskProject` tool is currently a stub.
We need to implement the **retrieval logic** that answers questions like "Why was the login changed?" by:
1.  **Vector Search**: Finding relevant code chunks.
2.  **Graph Traversal**: Finding *who* changed that code, *when*, and *why* (linked Issues).

## 2. Architecture
The logic resides in `internal/search/`.
-   **`search.go`**: High-level orchestration.
-   **`graph.go`** (New): Graph query construction and parsing.

### Data Model (Existing in SurrealDB)
-   **Nodes**: `file_chunk` (has embedding), `file`, `commit`, `issue`, `author`.
-   **Edges**: 
    -   `file_chunk` -> `part_of` -> `file` (Implicit or Explicit, check schema)
    -   `commit` -> `changed` -> `file`
    -   `commit` -> `implements` -> `issue`

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

### Step 3: Refine `cmd/server/main.go`
-   Ensure the `AskProject` tool outputs the formatted string to the user.
-   (Optional) If specific JSON output is needed for a frontend, struct it accordingly, but for MCP `CallToolResultText` is standard.

## 4. Verification
Run the server and test with `ask_project("What recent changes were made to the search logic?")`.
**Success Criteria**:
-   Response contains code snippets.
-   Response contains **Commit Messages** and **Dates**.
-   Response contains **Issue IDs** if linked.
