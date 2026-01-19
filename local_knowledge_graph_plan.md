# Local Knowledge Graph with SurrealDB Plan

## Goal
Build a local, persistent knowledge graph of the project's evolution using SurrealDB. This allows for deep functional and causal analysis (e.g., "Who touched this logic?", "What files change together?").

## User Review Required
> [!IMPORTANT]
> **SurrealDB Setup**: Since `surreal` is not in the PATH, I will attempt to download the standalone Windows binary (`surreal.exe`) to the `_tools/` directory.
> **Python Dependencies**: I will install `surrealdb` and `GitPython` (or use subprocess) in the local environment.

## Proposed Changes

### 1. Tool Setup
#### [NEW] `_tools/surreal.exe`
- Download the official SurrealDB binary for Windows.

### 2. Ingestion Agent
#### [NEW] [_tools/ingest_git.py](file:///c:/_dev/DeckOnLine/_tools/ingest_git.py)
- **Functionality**:
    - Starts the local SurrealDB instance (`surreal start --user root --pass root file:project.db`).
    - Connects via WebSocket.
    - Iterates through `git log`.
    - **Nodes**: `commit`, `file`, `author`.
    - **Edges**:
        - `commit` -> `changed` -> `file` (with diff stats).
        - `author` -> `authored` -> `commit`.
        - `commit` -> `parent_of` -> `commit`.

### 3. Analysis Agent
#### [NEW] [_tools/analyze_graph.py](file:///c:/_dev/DeckOnLine/_tools/analyze_graph.py)
- **Functionality**:
    - Connects to the running DB.
    - Runs graph queries, e.g.:
        - **Logical Coupling**: Find files that are frequently modified in the same commit.
        - **Expertise**: Who has modified `Documento.aspx` the most?

## Verification Plan
### Automated Tests
1.  Run `ingest_git.py` (small sample).
2.  Run `analyze_graph.py` and check for output.

### Manual Verification
- Inspect the generated `project.db` folder (SurrealDB storage).
