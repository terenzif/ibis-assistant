# Knowledge Server (MCP)

**Knowledge Server** è un server MCP (Model Context Protocol) avanzato progettato per agire come una "memoria vivente" del progetto. Integra l'analisi strutturale del codice (Git), la comprensione semantica (RAG vettoriale) e l'intento gestionale (Redmine) in un unico grafo della conoscenza interrogabile.

Il suo scopo principale è fornire agli agenti AI (come Claude, Copilot o Gemini) un contesto profondo che va oltre la semplice lettura dei file attuali, permettendo risposte basate su *storia*, *evoluzione* e *ragionamento*.

## 🌟 Caratteristiche Principali

*   **Grafo della Conoscenza Unificato**: Collega File, Commit, Autori, Branch e Issue in un database a grafo (SurrealDB).
*   **Git Ingestion**: Analizza la storia di Git per costruire relazioni causali (`commit` -> `changed` -> `file`).
*   **Integrazione Redmine**: Collega le modifiche del codice ai ticket di gestione (`commit` -> `implements` -> `issue`), permettendo di capire *perché* una modifica è stata fatta.
*   **Vector RAG Efficiente**: Utilizza l'API Batch di Gemini (`batchEmbedContents`) per generare embedding di centinaia di chunk in una singola chiamata, ottimizzando costi e latenza.
*   **Ricerca Ibrida**: Supporta query che combinano struttura (grafo) e semantica (vettori).
*   **MCP Compliant**: Espone strumenti standardizzati per l'integrazione plug-and-play con client MCP.

## 🛠️ Installazione

### Prerequisiti

*   **Go** 1.22 o superiore
*   **SurrealDB**: Il database backend (deve essere in esecuzione).
*   **Git**: Installato e accessibile da terminale.

### Setup

1.  **Clona il repository**:
    ```bash
    git clone <tuo-repo>
    cd knowledge_server
    ```

2.  **Compila il server**:
    ```bash
    go build -o knowledge_server.exe .
    ```

3.  **Avvia SurrealDB**:
    Assicurati che SurrealDB sia attivo. Per test locali:
    ```bash
    surreal start --user root --pass root file:project.db
    ```

## 🚀 Utilizzo

### Esecuzione del Server

Il server può essere eseguito in modalità SSE (Server-Sent Events) o Stdio.

```bash
# Esempio: Analizza il repo corrente e avvia in modalità SSE
./knowledge_server.exe -mode sse -port 3030 -repos "C:/path/to/my/repo"
```

### Configurazione Client MCP (es. Claude Desktop)

Aggiungi la configurazione al tuo file `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "knowledge-graph": {
      "command": "c:/path/to/knowledge_server.exe",
      "args": [
        "-mode", "stdio",
        "-repos", "c:/path/to/target_repo"
      ]
    }
  }
}
```

## 🧪 Test ed Evoluzione

### Esecuzione dei Test
Il progetto include una suite di test unitari per verificare la logica di schema e ingestione.

```bash
go test ./...
```

### Roadmap Evolutiva

1.  **Completamento Client DB**: L'attuale `internal/db/client.go` contiene placeholder per la connessione reale a SurrealDB. Il prossimo passo è abilitare l'autenticazione e le query reali.
2.  **Ingestione Redmine**: Implementare il polling delle API di Redmine per popolare i nodi `issue`.
3.  **Vector RAG**: Integrare il calcolo degli embedding per i file modificati (Delta RAG).
4.  **Agenti Autonomi**: Creare workflow che permettano al server di aggiornarsi automaticamente al push di nuovi commit.

## 📂 Struttura del Progetto

*   `cmd/`: Entry point dell'applicazione.
*   `internal/`: Codice sorgente interno.
    *   `schema/`: Definizioni delle costanti e strutture del grafo.
    *   `ingest/`: Logica di importazione (Git, Redmine, Code).
    *   `db/`: Wrapper per SurrealDB.
    *   `search/`: Logica di interrogazione del grafo.

---
*Progetto sviluppato come parte dell'iniziativa "Universal Senior Engineer".*
