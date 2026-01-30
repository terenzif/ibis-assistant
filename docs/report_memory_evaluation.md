# Evaluation: Adapting "Why Similarity Isn’t Enough for Memory" to Knowledge Server

## Executive Summary
The article "Why Similarity Isn’t Enough for Memory" argues that simple vector-based retrieval (RAG) is insufficient for robust agentic memory because it lacks **temporal awareness**, **conflict resolution**, and **structured relationships**. 

The current `knowledge-server` implementation has the **structural foundation** (a Knowledge Graph in SurrealDB and Git/Redmine ingestion) but currently relies almost exclusively on stateless **vector similarity** for retrieval.

## Gap Analysis

| Feature | Article Requirement | Current `knowledge-server` Status | Gap |
| :--- | :--- | :--- | :--- |
| **Retrieval** | Hybrid (Vector + Graph) | Vector Only (`internal/search/search.go` performs cosine similarity on `file_chunk`) | **High**: Graph connections (Commit, Issue, Author) are ignored during search. |
| **Short-term Memory** | Session Context (Current conversation) | None (Stateless API) | **High**: No concept of "Session" or "Conversation History". |
| **Long-term Memory** | Semantic (Facts) + Episodic (Experience) | Partial (Codebase is "Semantic", but no "Episodic" user history) | **Medium**: No storage of user interactions or learned preferences. |
| **Temporal Logic** | "Truth" vs "History" (handling updates) | Git History exists, but Retrieval doesn't prioritize "Head" or resolve conflicts. | **Medium**: Data exists (Git DAG), but query logic doesn't use it to filter/rank. |
| **Weighted Memory** | Usage-based weighting | None | **Low**: No mechanism to reinforce useful paths. |

## Proposed Implementation Roadmap

To apply the arguments from the article, we propose the following evolutionary steps for the Knowledge Server:

### Phase 1: Activate the Graph (The "Cognify" Step)
*Goal: Move from "Vector Search" to "Graph-Augmented Search".*

1.  **Enhance `AskProject`**:
    *   Perform Vector Search to find entry points (File Chunks).
    *   **Traverse**: From the best chunks, traverse edges (`changed`, `implements`, `authored`) to fetch:
        *   **Related Issues**: Why was this code written? (Intent)
        *   **Recent Commits**: Who changed it and when? (Context)
        *   **Related Files**: What else was changed in the same commit? (Coupling)
    *   **Rerank/Synthesize**: Use this graph context to generate a richer answer.

### Phase 2: Temporal & Conflict Awareness
*Goal: Ensure the agent understands the "latest" state vs "historical" state.*

1.  **Timeline Filtering**:
    *   When retrieving code, explicitly check if the file chunk belongs to the `HEAD` of the branch.
    *   Downgrade or flag results that are from older commits if they contradict newer ones (implicit in Git, but explicit in RAG response).
2.  **Deprecation Detection**:
    *   If a newer commit "removes" code that matched the vector search, the system should know it's "deleted" logic.

### Phase 3: Session & Episodic Memory
*Goal: Make the agent "remember" the user.*

1.  **Session Management**:
    *   Introduce a `Conversation` and `Message` table in SurrealDB.
    *   Store the user's last few queries to resolve pronouns or context (e.g., "Rewrite *that* function").
2.  **Feedback Loop**:
    *   Add a feedback mechanism (e.g., "Good answer" / "Bad answer").
    *   Store positive retrieval paths (Query -> File -> Issue) to reinforce them (Simple "Hebbian" learning).

## Conclusion
The `knowledge-server` is well-positioned to implement the "Cognee" approach described in the article because it already uses **SurrealDB**, which natively supports both Vectors and Graph traversals. The missing piece is the **application logic** in `internal/search` to leverage the graph data that `internal/ingest` is already populating.
