# Knowledge Server (MCP)

**Knowledge Server** è un server MCP (Model Context Protocol) avanzato progettato per agire come una "memoria vivente" del progetto. Integra l'analisi strutturale del codice (Git), la comprensione semantica (RAG vettoriale) e l'intento gestionale (Redmine) in un unico grafo della conoscenza interrogabile.

Il suo scopo principale è fornire agli agenti AI (come Claude, Copilot o Gemini) un contesto profondo che va oltre la semplice lettura dei file attuali, permettendo risposte basate su *storia*, *evoluzione* e *ragionamento*.

## 🌟 Caratteristiche Principali

* **Grafo della Conoscenza Unificato**: Collega File, Commit, Autori, Branch e Issue in un database a grafo (SurrealDB).
* **Git Ingestion**: Analizza la storia di Git per costruire relazioni causali (`commit` -> `changed` -> `file`).
* **Integrazione Redmine**: Collega le modifiche del codice ai ticket di gestione (`commit` -> `implements` -> `issue`), permettendo di capire *perché* una modifica è stata fatta.
* **Vector RAG Efficiente**: Utilizza l'API Batch di Gemini (`batchEmbedContents`) per generare embedding di centinaia di chunk in una singola chiamata, ottimizzando costi e latenza.
* **Ricerca Ibrida**: Supporta query che combinano struttura (grafo) e semantica (vettori).
* **MCP Compliant**: Espone strumenti standardizzati per l'integrazione plug-and-play con client MCP.

## 🛠️ Installazione

### Prerequisiti

* **Go** 1.22 o superiore
* **SurrealDB**: Il database backend.
  * *Opzione Automatica*: Se l'eseguibile `surreal.exe` è presente nella root del progetto, il server tenterà di avviarlo automaticamente.
    
    > **Download**: Scarica la versione Windows da [surrealdb.com/install](https://surrealdb.com/install) o dai [Release di GitHub](https://github.com/surrealdb/surrealdb/releases), estrai e copia `surreal.exe` nella cartella di questo progetto.
  * *Opzione Manuale*: SurrealDB pre-installato e in esecuzione.
* **Git**: Installato e accessibile da terminale.
* **Gemini API Keys**: È possibile specificare una lista di chiavi API nel file di configurazione (`gemini_keys`) o via env (`GEMINI_API_KEY` separati da virgola).
  * *Comportamento Multi-Key*: Se vengono fornite più chiavi, il server le utilizzerà in modalità **Round-Robin**. Questo permette di distribuire il carico e superare i limiti di Rate Limit (RPM) imposti da Google, aumentando parallelamente il throughput di ingestione (es. 2 chiavi = 2x throughput).

### Setup

1. **Clona il repository**:
   
   ```bash
   git clone <tuo-repo>
   cd knowledge_server
   ```

2. **Compila il server**:
   
   ```bash
   go build -o knowledge_server.exe ./cmd/server
   ```

3. **Configurazione**:
   Il server cerca un file `config.json` nella directory di esecuzione. Un file di esempio è stato creato.
   Modifica `config.json` con le tue chiavi e preferenze:
   
   ```json
   {
     "port": 3030,
     "mode": "sse",
     "db_user": "root",
     "db_password": "root",
     "gemini_keys": ["YOUR_API_KEY_HERE"],
     "redmine_url": "https://redmine.tuodominio.com",
     "redmine_key": "",
     "auto_scan": true
   }
   ```
   
   > **Nota su Redmine**: La chiave `redmine_key` nel config è la **System Key**, utilizzata per operazioni di background (es. ingestion automatica). Per operazioni utente (es. aggiornare un ticket), il client deve fornire la propria chiave via header `X-Redmine-API-Key`.

4. **Avvia il Server**:
   Se `surreal.exe` è nella cartella, l'avvio standard è:
   
   ```bash
   ./knowledge_server.exe /run
   ```
   
   Eseguendo `knowledge_server.exe` senza parametri verrà mostrata la guida rapida ai comandi.

5. **Installazione come Servizio Windows**:
   È possibile installare il server come servizio di sistema per un avvio automatico:

   ```bash
   # Installa il servizio (Richiede privilegi di Amministratore)
   ./knowledge_server.exe /install

   # Disinstalla il servizio
   ./knowledge_server.exe /uninstall
   ```
   Il servizio verrà configurato con il nome "knowledge-server" e descrizione appropriata.

## 🚀 Utilizzo

### Esecuzione del Server

Il server supporta tre comandi principali:

* `/run`: Avvia il server interattivamente (comportamento standard MCP).
* `/install`: Installa il server come servizio Windows "knowledge-server".
* `/uninstall`: Rimuove il servizio Windows.

Può essere eseguito in modalità SSE (Server-Sent Events) o Stdio tramite i flag (da usare con `/run`):

```bash
# Esempio: Analizza il repo corrente e avvia in modalità SSE
./knowledge_server.exe /run -mode sse -port 3030 -scan
```

### ⚙️ Configurazione e Precedenza
Il server segue una gerarchia di priorità per la configurazione:
1. **Flag da riga di comando** (es. `-port 9000`) - Priorità massima.
2. **Variabili d'ambiente** (es. `PORT=9000`).
3. **File di configurazione** (`config.json`).
4. **Default predefiniti**.

Il file `config.json` viene cercato automaticamente nella directory corrente e nella directory in cui si trova l'eseguibile (utile quando eseguito come servizio). È possibile specificare un file diverso con il flag `-config <path>`.

### 🔌 Integrazione Client (Centralizzata)

Il Knowledge Server è installato centralmente su **`localhost`** e funge da oracolo per tutto il team. I client non devono eseguire nulla in locale, ma solo connettersi all'endpoint SSE.

**Endpoint Pubblico:** `http://localhost:3030/sse`

### 🔑 Autenticazione Utente (Redmine)

Per eseguire azioni che richiedono l'identità dell'utente (es. `redmine_update_issue`, `search_my_issues`), il client SSE **deve** passare la chiave API Redmine dell'utente corrente tramite l'header HTTP:

`X-Redmine-API-Key: <USER_API_KEY>`

Se questo header non è presente, il server utilizzerà la *System Key* (sola lettura/globale) definita in `config.json`. Le azioni di scrittura falliranno senza una chiave utente valida.

#### 1. Claude Desktop

Configura il client per connettersi allo stream SSE remoto.
File: `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "knowledge-graph": {
      "url": "http://localhost:3030/sse",
      "headers": {
        "X-Redmine-API-Key": "YOUR_USER_KEY"
      }
    }
  }
}
```
*Nota: Il supporto per gli header personalizzati dipende dalla versione di Claude Desktop / Client MCP.*

*Nota*: Se la versione corrente di Claude Desktop supporta solo Stdio, utilizzare un bridge locale o attendere l'aggiornamento.

#### 2. Visual Studio Code

Estensioni come **Roo Code** o **Model Context Protocol** supportano connessioni remote.
Nel `settings.json` o nelle impostazioni dell'estensione:

```json
"mcpServers": {
  "remote-knowledge": {
    "url": "http://localhost:3030/sse",
    "transport": "sse",
    "headers": {
      "X-Redmine-API-Key": "YOUR_USER_KEY"
    }
  }
}
```

Questo permette a tutti gli sviluppatori di accedere allo stesso grafo condiviso senza indicizzare i file localmente.

#### 3. Visual Studio 2022

L'integrazione nativa di MCP in Visual Studio è in fase di evoluzione rapida.

* **Copilot Chat**: Se avete accesso a Copilot Chat in VS, verificate nei setting se supporta "External Context Providers".
* **Web Inspector (Consigliato)**: La soluzione più robusta oggi è utilizzare l'Inspector come "Chat Companion" dedicata al progetto.
  1. Apri un terminale.
  2. Esegui: `npx @modelcontextprotocol/inspector http://localhost:3030/sse`
  3. Tieni la finestra del browser (Inspector) affiancata al codice.

#### 4. SSMS (SQL Server Management Studio)

Non esiste ancora un supporto nativo *ufficiale* per MCP in SSMS.

* **Copilot in SSMS**: Se attivo, utilizza il contesto di Azure/Github standard. Non è garantito che "veda" il server MCP locale/remoto.
* **Workflow "Side-by-Side"**: Gli sviluppatori SQL devono tenere aperto il **Web Inspector** (vedi punto 3) o **Claude Desktop**.
  * *Scenario*: Incollate la definizione di una vista complessa nel Inspector e chiedete: *"Spiegami la logica di business dietro questa vista basandoti sulle Issue Redmine"*.

#### 5. Antigravity & Agenti CI/CD

Gli agenti automatici (o Antigravity) configurati nella rete aziendale possono contattare direttamente l'endpoint.

* **Discovery**: `http://localhost:3030`
* **Vantaggio**: L'agente non deve clonare il repo per capirlo; chiede al Knowledge Server centrale che ha già "digerito" tutto il codice e la storia.

> **Nota Troubleshooting**: Essendo il server su `win-dev`, assicuratevi che il firewall di Windows su quella macchina permetta il traffico in ingresso sulla porta **3030**.



---

## 💡 Casi d'Uso Concreti

Ecco alcuni scenari reali in cui il Knowledge Server fa la differenza:

1. **Onboarding su Codice Legacy**:
   
   * *Domanda*: "Cosa fa il modulo `GestionePrezzi` e qual è la sua storia?"
   * *Risultato*: Il server recupera il README, i commit principali degli ultimi 2 anni, e le issue Redmine collegate, offrendo un riassunto narrativo dell'evoluzione del modulo.

2. **Debugging "Chi ha rotto cosa?"**:
   
   * *Domanda*: "Perché la funzione `CalcolaSconto` è cambiata la settimana scorsa?"
   * *Risultato*: Il server trova il commit specifico, legge il messaggio di commit ("Fix bug #402 urgent"), e recupera il ticket Redmine #402 che spiega il bug aziendale sottostante.

3. **Analisi di Impatto**:
   
   * *Domanda*: "Se tocco la tabella `Listini`, quali file .cs la usano?"
   * *Risultato*: Grazie alla ricerca full-text e vettoriale, identifica tutti i riferimenti nel codice e fornisce un elenco dei file a rischio regressione.

4. **Recupero Conoscenza Persa**:
   
   * *Domanda*: "Esiste già una logica per l'invio mail PEC?"
   * *Risultato*: La ricerca semantica (RAG) trova pezzi di codice anche se chiamati `SendSecureMessage` invece di `PEC`, basandosi sulla similitudine concettuale degli embedding.

## 🧪 Test

### Esecuzione dei Test

Il progetto include una suite di test unitari per verificare la logica di schema e ingestione.

```bash
go test ./...
```

## 📂 Struttura del Progetto

* `cmd/`: Entry point dell'applicazione.
* `internal/`: Codice sorgente interno.
  * `schema/`: Definizioni delle costanti e strutture del grafo.
  * `ingest/`: Logica di importazione (Git, Redmine, Code).
  * `db/`: Wrapper per SurrealDB.
  * `search/`: Logica di interrogazione del grafo.

---

*Progetto sviluppato come parte dell'iniziativa "Universal Senior Engineer".*
