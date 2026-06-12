# Cognitive Architecture Specification: Adaptive Ibis Assistant
**Target Developer:** Jules
**Status:** Ingestion Complete. Cognitive Logic Pending.
**Objective:** Implement a "Cognitive" Retrieval System that combines Vector Similarity, Graph Structure, Temporal Recency, and Usage-Based Reinforcement (Weighted Memory).

## 1. Core Philosophy
The system must move beyond static retrieval. It must "learn" which paths in the graph are valuable based on usage.
1.  **Vector (Identity)**: "This code *looks like* what you asked for."
2.  **Graph (Context)**: "This code was *changed by* X *to fix* Y."
3.  **Temporal (Recency)**: "This information is *current* (or clearly marked as *historical*)."
4.  **Weight (Experience)**: "This connection was *useful* in previous similar queries."

## 2. Data Model Enhancements (Schema)
Jules, you need to extend the current schema logic in your queries (SurrealDB is schemaless-friendly, but be explicit in your code).

### 2.1. Weighted Edges
*   **`changed` Edge (Commit -> File)**: Add `impact` (float 0.0-1.0).
    *   *Logic*: Calculate based on lines changed / total lines. Big refactors = High Impact.
*   **`implements` Edge (Commit -> Issue)**: Add `confidence` (float 0.0-1.0).
    *   *Logic*: 1.0 if explicit "Fixes #123". 0.5 if just mentioned "#123".

### 2.2. Reinforcement Attributes (The "Memify" Layer)
*   **`file_chunk` Nodes**: Add `access_count` (int) and `last_accessed` (datetime).
*   **`implements` Edges**: Add `usage_weight` (float, default 1.0).
    *   *Logic*: Increment this when a user explicitly upvotes a result or follows a link.

## 3. Retrieval Algorithm (`AskProject`)

### Step 1: Hybrid Scoring (Vector + Time)
When querying `file_chunk`, use a **Time-Decayed Score**:
```sql
SELECT 
    id, 
    content, 
    file_path,
    (vector::similarity::cosine(embedding, $vec) * 0.7) + 
    (math::max(0, 1 - (time::now() - created_at).days / 365) * 0.3) 
    as hybrid_score 
FROM file_chunk 
ORDER BY hybrid_score DESC 
LIMIT 10;
```
*Note: Adjust weights (0.7/0.3) as needed. Newer chunks rank higher.*

### Step 2: Graph Spreading (Context Expansion)
For the top results, traverse the **Weighted Graph**:
1.  **Find Intent**: Traverse `<-changed<-commit->implements->issue`.
    *   *Filter*: Only follow edges where `usage_weight > 0.5` (prune useless noise).
2.  **Find Experts**: Traverse `<-changed<-commit<-authored<-author`.
    *   *Aggregate*: Who touched this file most *recently* AND with *high impact*?

### Step 3: Response Construction
Return a **Structured JSON** object for the AI agent:
```json
{
  "results": [
    {
      "code_chunk_id": "file_chunk:z8a...",
      "file_path": "internal/auth/auth.go",
      "relevance_score": 0.92,  // Vector + Time
      "is_current": true,       // False if file was deleted in HEAD
      "context": {
        "related_issues": [
          {"id": "issue:102", "subject": "Fix JWT overflow", "weight": 1.5} // High weight = Reinforced
        ],
        "expert_authors": ["jules", "admin"]
      }
    }
  ]
}
```

## 4. Feedback Loop (The "Learning" System)
**New Tool Required**: `reinforce_path`
Jules, implement a tool that allows the AI Agent to "tell" the server what was useful.

*   **Tool**: `reinforce_path(source_node, target_node, feedback_score)`
*   **Logic**:
    1.  If `feedback_score > 0` (Positive):
        *   `UPDATE $edge SET usage_weight += 0.1;`
        *   `UPDATE $target_node SET access_count += 1;`
    2.  If `feedback_score < 0` (Negative):
        *   `UPDATE $edge SET usage_weight -= 0.1;` (Decay bad paths)

## 5. Implementation Roadmap
1.  **Refactor `AskProject`**: Implement the **Time-Decayed** Vector Search query.
2.  **Implement `GraphWalker`**: A Go struct in `internal/search/graph.go` that performs the weighted traversals.
3.  **Implement `Reinforce` Tool**: The API endpoint/MCP tool for the feedback loop.
