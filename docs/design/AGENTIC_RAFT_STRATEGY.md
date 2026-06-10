# Agentic RAFT Strategy

**Status:** Draft
**Objective:** Upgrade the Ibis Arc from a passive retrieval system to an active, self-optimizing agentic system.

## 1. Core Concepts

### 1.1. Agentic Search (The "Brain")
The current `AskProject` implementation performs a single-pass Hybrid Search (Vector + Graph). This fails when the user's question is complex, ambiguous, or requires multi-step reasoning (e.g., "Trace the authentication flow and identify where the session timeout is validated").

**Solution:** Implement a **ReAct (Reason + Act)** loop within the server.
1.  **Analyze**: The AI analyzes the user's query.
2.  **Plan**: Determines if it needs to search for file content, specific commits, or issue context.
3.  **Act**: Executes `AskProject` (retrieval).
4.  **Observe**: Analyzes the results. If insufficient, it refines the search or traverses the graph.
5.  **Synthesize**: Generates a final, comprehensive answer.

### 1.2. Self-Optimization via RAFT (The "Gym")
**RAFT (Retrieval Augmented Fine Tuning)** typically involves fine-tuning an LLM on domain-specific documents. We adapt this for our **Graph-RAG** system as **"Synthetic Reinforcement"**.

**Problem:** The knowledge graph relies on user usage (`reinforce_path`) to learn what is important. If users don't vote, the graph doesn't learn.

**Solution:** A background "Dreaming" process.
1.  **Selection**: Pick a random code chunk (prioritizing low-access or high-churn areas).
2.  **Generation**: Use an LLM to generate a synthetic "User Question" that this chunk answers.
    *   *Example:* Chunk is `func ValidateToken()`. Question: "How is the JWT token validated?"
3.  **Retrieval**: Run `AskProject` with the synthetic question.
4.  **Evaluation**: Check if the source chunk appears in the top results.
5.  **Reinforcement**:
    *   **Success**: Positive Reinforcement (`reinforce_path` with score +1).
    *   **Failure**: Identify why. (Future: Create a "synthetic link" between keywords and the chunk).

## 2. Technical Architecture

### 2.1. AI Client Upgrade
*   **Requirement**: The current `Embedder` interface is insufficient. We need `Generative` capabilities (Chat).
*   **Action**: Implement `GenerateContent` in `internal/ai/gemini.go`.

### 2.2. Agentic Search Implementation
*   **New Function**: `AskProjectAgentic(ctx, query)`
*   **Logic**:
    ```go
    history := []Message{{Role: "system", Content: "You are a senior engineer..."}}
    history = append(history, Message{Role: "user", Content: query})

    for step := 0; step < 3; step++ {
        response := AI.Generate(history)
        if response.Contains("FINAL ANSWER") {
             return response
        }
        if response.Contains("SEARCH: <term>") {
             results := Search(term)
             history = append(history, Message{Role: "tool", Content: results})
        }
    }
    ```

### 2.3. Optimization Loop (`optimize_knowledge`)
*   **New Tool**: `optimize_knowledge(count)`
*   **Logic**:
    ```go
    for i := 0; i < count; i++ {
        chunk := DB.GetRandomChunk()
        qa := AI.GenerateQA(chunk.Content)
        results := Search(qa.Question)
        if results.Contains(chunk.ID) {
            Reinforce(chunk.ID, 1.0)
        }
    }
    ```

## 3. Benefits
*   **Higher Accuracy**: Multi-step reasoning handles complex queries better.
*   **Cold Start Problem Solved**: The system "trains itself" overnight using RAFT, meaning the graph becomes useful even before real users start asking questions.
