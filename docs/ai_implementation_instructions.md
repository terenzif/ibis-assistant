# Phased Implementation Guide: AI API Generalization (Top-Down Methodology)

> [!NOTE]
> **Implementation status: COMPLETE AND VALIDATED (June 2026)**
> All steps in this document have been executed successfully. The generalized architecture for embedding and reasoning providers, the local Ollama Runner, and on-demand installation for Windows/Linux/macOS have been implemented and integrated.

---

This operational guide defines the implementation plan in **incremental, sequential phases**. Each phase is a self-contained milestone with intermediate build and test checks, designed to guide coding agents and avoid hallucinations or context loss caused by model limits.

---

## Phase Overview
* **Phase 1:** Core Definition and Abstractions (Interfaces & Orchestrator Client)
* **Phase 2:** Configuration Update (JSON Schema and Go Parser)
* **Phase 3:** Gemini Adapter Migration (Refactor existing code)
* **Phase 4:** Ollama Adapter Implementation (Local API requests)
* **Phase 5:** Ollama Daemon Runner Implementation (Lifecycle and Auto-Pull)
* **Phase 6:** Wiring and Initialization (Main Loop and Graceful Shutdown)
* **Phase 7:** Validation and Unit Testing

---

## Phase 1: Core Definition and Abstractions
**Goal:** Create abstractions without breaking compilation of the rest of the app (e.g. `internal/search` or `BatchManager`).

### Operational steps:
1. Create `internal/ai/types.go` and move required data structures into it (e.g. `Content`, `Part`, `GenerationConfig`, `Candidate`, `UsageMetadata`) extracted from the current `gemini.go`.
2. Declare the `EmbeddingProvider` and `ReasoningProvider` interfaces in `types.go`:
   ```go
   package ai

   import "context"

   type EmbeddingProvider interface {
       Name() string
       EmbedText(ctx context.Context, text string) ([]float32, error)
       BatchEmbedText(ctx context.Context, texts []string) ([][]float32, error)
   }

   type ReasoningProvider interface {
       Name() string
       GenerateContent(ctx context.Context, contents []Content, config GenerationConfig) (Candidate, error)
   }
   ```
3. Create `internal/ai/client.go` and implement the `Client` struct as orchestrator. It must act as a compatible adapter for the interface currently used by `search.Service` and `BatchManager`:
   ```go
   package ai

   import "context"

   type Client struct {
       embedding EmbeddingProvider
       reasoning ReasoningProvider
       runner    *OllamaRunner // Implemented in Phase 5
   }

   func NewClient(emb EmbeddingProvider, reas ReasoningProvider, runner *OllamaRunner) *Client {
       return &Client{
           embedding: emb,
           reasoning: reas,
           runner:    runner,
       }
   }

   func (c *Client) EmbedText(ctx context.Context, text string) ([]float32, error) {
       return c.embedding.EmbedText(ctx, text)
   }

   func (c *Client) BatchEmbedText(ctx context.Context, texts []string) ([][]float32, error) {
       return c.embedding.BatchEmbedText(ctx, texts)
   }

   func (c *Client) GenerateContent(ctx context.Context, contents []Content, config GenerationConfig) (Candidate, error) {
       return c.reasoning.GenerateContent(ctx, contents, config)
   }

   func (c *Client) IsFunctional() bool {
       return c.embedding != nil && c.reasoning != nil
   }

   func (c *Client) Stop() {
       if c.runner != nil {
           c.runner.Stop()
       }
   }
   ```

### Phase 1 checkpoint:
> [!NOTE]
> Run `go build ./internal/ai/...` to ensure the new interfaces and client compile without errors.

---

## Phase 2: Configuration Update
**Goal:** Extend configuration to support different providers.

### Operational steps:
1. Open `config_master.json` and replace the `gemini_keys` block with the new nested structure under the `ai` key:
   ```json
   "ai": {
     "embedding": {
       "provider": "ollama",
       "model": "nomic-embed-text",
       "url": "http://127.0.0.1:11434",
       "auto_start": true,
       "auto_update": true
     },
     "reasoning": {
       "provider": "gemini",
       "model": "gemini-2.5-pro",
       "keys": [
         {
           "key": "",
           "rpm": 100,
           "tpm": 30000,
           "rpd": 1000,
           "owner": "local"
         }
       ]
     }
   }
   ```
2. Update `config_master.go` (or `config/config.go`) to map these new JSON keys to Go structs:
   ```go
   type AIConfig struct {
       Embedding EmbeddingConfig `json:"embedding"`
       Reasoning ReasoningConfig `json:"reasoning"`
   }

   type EmbeddingConfig struct {
       Provider   string `json:"provider"`
       Model      string `json:"model"`
       URL        string `json:"url"`
       AutoStart  bool   `json:"auto_start"`
       AutoUpdate bool   `json:"auto_update"`
   }

   type KeyConfig struct {
       Key           string        `json:"key"`
       RPM           int           `json:"rpm"`
       TPM           int           `json:"tpm"`
       RPD           int           `json:"rpd"`
       Owner         string        `json:"owner"`
       FlushInterval time.Duration `json:"-"`
       AllowOverage  bool          `json:"-"`
   }

   type ReasoningConfig struct {
       Provider string      `json:"provider"`
       Model    string      `json:"model"`
       Keys     []KeyConfig `json:"keys"`
   }
   ```
3. Add the `AI AIConfig` field to the main `Config` struct and update parsing / environment variable loading in `Load()`.

### Phase 2 checkpoint:
> [!NOTE]
> Run `go build ./...` to ensure configuration parsing introduces no regressions.

---

## Phase 3: Gemini Adapter Migration
**Goal:** Isolate proprietary Gemini logic inherited from the old client.

### Operational steps:
1. Rename the internal `Client` struct to `GeminiProvider` inside `internal/ai/gemini.go`.
2. Make `GeminiProvider` implement both `EmbeddingProvider` and `ReasoningProvider`.
3. Adapt constructors in `gemini.go`:
   ```go
   func NewGeminiProvider(apiKeys []KeyConfig, dbClient db.Executor) *GeminiProvider
   ```
4. Ensure delegated methods (`EmbedText`, `BatchEmbedText`, `GenerateContent`) correctly manage the internal worker pool.

### Phase 3 checkpoint:
> [!NOTE]
> Run `go test ./internal/ai/...` to verify old Gemini tests compile and only fail due to the changed initialization if at all.

---

## Phase 4: Ollama Adapter Implementation
**Goal:** Write the client logic to connect to local Ollama.

### Operational steps:
1. Create `internal/ai/ollama.go`.
2. Implement `OllamaProvider` with support for `/api/embeddings`:
   ```go
   package ai

   import (
       "bytes"
       "context"
       "encoding/json"
       "fmt"
       "net/http"
       "time"
   )

   type OllamaProvider struct {
       baseURL string
       model   string
       client  *http.Client
   }

   func NewOllamaProvider(baseURL, model string) *OllamaProvider {
       return &OllamaProvider{
           baseURL: baseURL,
           model:   model,
           client:  &http.Client{Timeout: 30 * time.Second},
       }
   }

   func (p *OllamaProvider) Name() string { return "ollama" }
   ```
3. Implement `EmbedText(ctx, text)` and `BatchEmbedText(ctx, texts)`. For batch, you can send limited concurrent or sequential requests to Ollama (Ollama processes embedding requests quickly and locally).

### Phase 4 checkpoint:
> [!NOTE]
> Run `go build ./internal/ai/...` to confirm the Ollama adapter compiles correctly.

---

## Phase 5: Ollama Daemon Runner Implementation
**Goal:** Download, start, and manage Ollama in the background transparently.

### Operational steps:
1. Create `internal/ai/runner.go`.
2. Write on-demand download logic `EnsureOllama(autoUpdate bool)` by reading the Ollama GitHub releases API (`https://api.github.com/repos/ollama/ollama/releases/latest`), downloading the binary and unpacking it (use `internal/db/install.go` as a logic reference).
3. Implement the `OllamaRunner` struct:
   ```go
   type OllamaRunner struct {
       cmd *exec.Cmd
   }

   func NewOllamaRunner(autoUpdate bool) *OllamaRunner
   func (r *OllamaRunner) Start() error
   func (r *OllamaRunner) Stop() error
   ```
4. Write auto-pull logic: before completing `Start()`, call `POST /api/pull` with body `{"name": "nomic-embed-text"}` if the model does not appear in `GET /api/tags`.

### Phase 5 checkpoint:
> [!NOTE]
> Run `go build ./internal/ai/...` to ensure the runner and system calls compile correctly.

---

## Phase 6: Wiring and Initialization
**Goal:** Integrate the new generalized architecture at application startup.

### Operational steps:
1. Open `cmd/server/main.go`.
2. Find where the old `ai.NewClient` is instantiated.
3. Rewrite initialization to decode the master config file:
   - If the embedding provider is `ollama`, start the runner and instantiate `OllamaProvider`.
   - If the provider is `gemini`, instantiate `GeminiProvider` for embeddings.
   - Instantiate the reasoning provider (e.g. `GeminiProvider` using keys from the reasoning block).
   - Create the unified client: `aiClient := ai.NewClient(embProvider, reasProvider, runner)`.
4. In the server shutdown cycle, ensure `aiClient.Stop()` is invoked to cleanly stop the local Ollama Runner.

### Phase 6 checkpoint:
> [!NOTE]
> Run `go build ./cmd/server` to ensure the full server compiles correctly with the new contracts.

---

## Phase 7: Validation and Unit Testing
**Goal:** Ensure global system stability.

### Operational steps:
1. Run the global test suite: `go test ./...`.
2. Start the server locally with local SurrealDB and verify on startup that:
   * Local Ollama is started and the embedding model is downloaded.
   * The server correctly logs startup of both subsystems.
3. Perform a test commit ingestion or semantic search request to confirm vectors are stored and retrieved correctly from SurrealDB.
