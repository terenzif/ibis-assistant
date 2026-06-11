# Guida Operativa in Fasi: Generalizzazione delle API AI (Metodologia Top-Down)

> [!NOTE]
> **Stato dell'implementazione: COMPLETATO E VALIDATO (Giugno 2026)**
> Tutti i passaggi descritti in questo documento sono stati eseguiti con successo. L'architettura generalizzata per gli embedding e reasoning provider, l'Ollama Runner locale e l'installazione automatica on-demand per Windows/Linux/macOS sono stati implementati e integrati.

---

Questa guida operativa definisce il piano di implementazione suddiviso in **fasi incrementali e sequenziali**. Ciascuna fase rappresenta un traguardo autocontenuto con verifiche di compilazione e test intermedi, studiato per guidare gli agenti di coding ed evitare allucinazioni o perdite di contesto dovute ai limiti dei modelli.

---

## Panoramica delle Fasi
* **Fase 1:** Definizione del Core e Astrazioni (Interfacce & Client Orchestratore)
* **Fase 2:** Aggiornamento della Configurazione (JSON Schema e Parser Go)
* **Fase 3:** Migrazione dell'Adapter Gemini (Refactoring del codice esistente)
* **Fase 4:** Implementazione dell'Adapter Ollama (Richieste API locali)
* **Fase 5:** Implementazione dell'Ollama Daemon Runner (Lifecycle e Auto-Pull)
* **Fase 6:** Cablaggio e Inizializzazione (Main Loop e Graceful Shutdown)
* **Fase 7:** Validazione e Unit Testing

---

## Fase 1: Definizione del Core e Astrazioni
**Obiettivo:** Creare le astrazioni senza rompere la compilazione del resto dell'app (es. `internal/search` o `BatchManager`).

### Passi operativi:
1. Crea `internal/ai/types.go` e sposta al suo interno le strutture dati necessarie (es. `Content`, `Part`, `GenerationConfig`, `Candidate`, `UsageMetadata`) estratte dall'attuale `gemini.go`.
2. Dichiara le interfacce `EmbeddingProvider` e `ReasoningProvider` in `types.go`:
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
3. Crea `internal/ai/client.go` e implementa la struct `Client` che farà da orchestratore. Deve fungere da adapter compatibile con l'interfaccia usata attualmente da `search.Service` e `BatchManager`:
   ```go
   package ai

   import "context"

   type Client struct {
       embedding EmbeddingProvider
       reasoning ReasoningProvider
       runner    *OllamaRunner // Sarà implementato in Fase 5
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

### Checkpoint di Fase 1:
> [!NOTE]
> Esegui `go build ./internal/ai/...` per assicurarti che le nuove interfacce e il client compilino senza errori.

---

## Fase 2: Aggiornamento della Configurazione
**Obiettivo:** Estendere le configurazioni per supportare diversi provider.

### Passi operativi:
1. Apri `config_master.json` e sostituisci il blocco `gemini_keys` con la nuova struttura annidata sotto la chiave `ai`:
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
           "owner": "Fabbio"
         }
       ]
     }
   }
   ```
2. Modifica `config_master.go` (o `config/config.go`) per mappare queste nuove chiavi JSON in struct Go:
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
3. Aggiungi il campo `AI AIConfig` nella struct principale `Config` e aggiorna le logiche di parsing e caricamento delle variabili d'ambiente in `Load()`.

### Checkpoint di Fase 2:
> [!NOTE]
> Esegui `go build ./...` per assicurarti che il parsing della configurazione non introduca regressioni.

---

## Fase 3: Migrazione dell'Adapter Gemini
**Obiettivo:** Isolare la logica proprietaria di Gemini ereditata dal vecchio client.

### Passi operativi:
1. Rinomina la struct interna `Client` in `GeminiProvider` all'interno di `internal/ai/gemini.go`.
2. Fai in modo che `GeminiProvider` implementi sia `EmbeddingProvider` e `ReasoningProvider`.
3. Adatta i costruttori in `gemini.go`:
   ```go
   func NewGeminiProvider(apiKeys []KeyConfig, dbClient db.Executor) *GeminiProvider
   ```
4. Assicurati che i metodi delegati (`EmbedText`, `BatchEmbedText`, `GenerateContent`) gestiscano correttamente il worker pool interno.

### Checkpoint di Fase 3:
> [!NOTE]
> Esegui `go test ./internal/ai/...` per verificare che i vecchi test di Gemini compilino ed eventualmente falliscano solo a causa dell'inizializzazione modificata.

---

## Fase 4: Implementazione dell'Adapter Ollama
**Obiettivo:** Scrivere la logica client per connettersi ad Ollama locale.

### Passi operativi:
1. Crea `internal/ai/ollama.go`.
2. Implementa `OllamaProvider` con supporto per `/api/embeddings`:
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
3. Implementa i metodi `EmbedText(ctx, text)` e `BatchEmbedText(ctx, texts)`. Per il batch, puoi inviare richieste concorrenti limitate o sequenziali a Ollama (Ollama elabora le richieste di embedding velocemente e localmente).

### Checkpoint di Fase 4:
> [!NOTE]
> Esegui `go build ./internal/ai/...` per confermare che l'adapter Ollama compili correttamente.

---

## Fase 5: Implementazione dell'Ollama Daemon Runner
**Obiettivo:** Scaricare, avviare e gestire Ollama in background in modo trasparente.

### Passi operativi:
1. Crea `internal/ai/runner.go`.
2. Scrivi la logica di download on-demand `EnsureOllama(autoUpdate bool)` leggendo l'API dei rilasci GitHub di Ollama (`https://api.github.com/repos/ollama/ollama/releases/latest`), scaricando il binario e scompattandolo (usa come traccia logica `internal/db/install.go`).
3. Implementa la struct `OllamaRunner`:
   ```go
   type OllamaRunner struct {
       cmd *exec.Cmd
   }

   func NewOllamaRunner(autoUpdate bool) *OllamaRunner
   func (r *OllamaRunner) Start() error
   func (r *OllamaRunner) Stop() error
   ```
4. Scrivi la logica di auto-pull: prima di completare lo `Start()`, esegui una chiamata `POST /api/pull` con corpo `{"name": "nomic-embed-text"}` se il modello non compare nell'elenco di `GET /api/tags`.

### Checkpoint di Fase 5:
> [!NOTE]
> Esegui `go build ./internal/ai/...` per accertarti che il runner e le chiamate di sistema compilino correttamente.

---

## Fase 6: Cablaggio e Inizializzazione
**Obiettivo:** Integrare la nuova architettura generalizzata all'avvio dell'applicazione.

### Passi operativi:
1. Apri `cmd/server/main.go`.
2. Individua il punto in cui viene istanziato il vecchio `ai.NewClient`.
3. Riscrivi la logica di inizializzazione per decifrare il file di configurazione master:
   - Se il provider di embedding è `ollama`, avvia il runner e istanzia `OllamaProvider`.
   - Se il provider è `gemini`, istanzia `GeminiProvider` per gli embedding.
   - Istanzia il provider di reasoning (es. `GeminiProvider` usando le chiavi del blocco reasoning).
   - Crea il client unificato: `aiClient := ai.NewClient(embProvider, reasProvider, runner)`.
4. Nel ciclo di shutdown del server, assicurati che venga invocato `aiClient.Stop()` per fermare in modo pulito l'Ollama Runner locale.

### Checkpoint di Fase 6:
> [!NOTE]
> Esegui `go build ./cmd/server` per assicurarti che l'intero server compili correttamente con i nuovi contratti.

---

## Fase 7: Validazione e Unit Testing
**Obiettivo:** Assicurare la stabilità globale del sistema.

### Passi operativi:
1. Esegui la suite di test globale: `go test ./...`.
2. Avvia il server localmente con il db locale SurrealDB e verifica che all'avvio:
   * Venga avviato Ollama locale e scaricato il modello di embedding.
   * Il server logghi correttamente l'avvio di entrambi i sottosistemi.
3. Effettua una richiesta di test di ingestion dei commit o di ricerca semantica per accertare che i vettori vengano salvati e recuperati correttamente da SurrealDB.
