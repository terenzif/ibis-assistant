# Ibis Assistant (MCP)

**Ibis Assistant** è un server MCP (Model Context Protocol) avanzato progettato per agire come una "memoria vivente" del progetto. Integra l'analisi strutturale del codice (Git), la comprensione semantica (RAG vettoriale) e l'intento gestionale (ticketing multi-provider: Redmine/Jira/Azure DevOps) in un unico grafo della conoscenza interrogabile.

Il suo scopo principale è fornire agli agenti AI (come Claude, Copilot o Gemini) un contesto profondo che va oltre la semplice lettura dei file attuali, permettendo risposte basate su *storia*, *evoluzione* e *ragionamento*.

## 🌟 Caratteristiche Principali

* **Grafo della Conoscenza Unificato**: Collega File, Commit, Autori, Branch e Issue in un database a grafo (SurrealDB).
* **Git Ingestion**: Analizza la storia di Git per costruire relazioni causali (`commit` -> `changed` -> `file`).
* **Integrazione Ticketing Multi-Provider**: Collega le modifiche del codice ai ticket/work item (`commit` -> `implements` -> `issue`), permettendo di capire *perché* una modifica è stata fatta.
* **Analisi Log Avanzata**: Ingestione log e riconoscimento pattern d'errore (Error Type) unificati al grafo semantico con **persistenza dell'offset**.
* **Reporting Automatico**: Generazione di report periodici via email (SMTP) per anomalie e costi AI.
* **Vector RAG Efficiente**: Utilizza l'API Batch di Gemini (`batchEmbedContents`) per generare embedding di centinaia di chunk in una singola chiamata, ottimizzando costi e latenza.
* **Ricerca Ibrida**: Supporta query che combinano struttura (grafo) e semantica (vettori).
* **MCP Compliant**: Espone strumenti standardizzati per l'integrazione plug-and-play con client MCP.

## 🛠️ Installazione

### Prerequisiti

* **Go** 1.22 o superiore
* **SurrealDB**: Il database backend.
  * *Opzione Automatica (Consigliata)*: Il server gestisce automaticamente SurrealDB. Se `surreal.exe` non è presente, viene scaricato on-demand da GitHub.
* **Ollama (AI Embedding locale)**:
  * *Gestione Automatica (Consigliata)*: Se `auto_start` è abilitato in `config.json` e Ollama non è in esecuzione, il server rileva l'installazione locale. Se non è presente, **scarica l'installer o il binario ed esegue l'installazione in background**. Inoltre scarica on-demand il modello specificato (es. `nomic-embed-text`).
* **Git**: Installato e accessibile da terminale.
* **Chiavi API AI**:
  * **Embedding**: Gestito localmente via Ollama (default) o configurato per usare Gemini.
  * **Reasoning**: Gestito tramite Gemini con supporto Multi-Key (Priority/Failover) inserendo le chiavi in `ai.reasoning.keys` (con supporto di retrocompatibilità per il vecchio campo `gemini_keys` a livello root).

### Setup

1. **Clona il repository**:
   
   ```bash
   git clone <tuo-repo>
   cd ibis-assistant
   ```

2. **Compila il server**:
   
   ```bash
   go build -o ibis-assistant.exe ./cmd/server
   ```

3. **Configurazione**:
   Il server cerca un file `config.json` nella directory di esecuzione. Un file di esempio è `config_master.json`.
   Modifica `config.json` con le tue chiavi e preferenze:
   
   ```json
   {
     "port": 3333,
     "mode": "sse",
     "db_url": "ws://127.0.0.1:8000/rpc",
     "db_user": "root",
     "db_password": "root",
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
           { "key": "", "rpm": 15, "tpm": 30000, "rpd": 1500, "owner": "default" }
         ]
       }
     },
     "redmine_url": "https://redmine.tuodominio.com",
     "redmine_key": "",
     "db_auto_update": true,
     "auto_scan": true,
     "logs_root": "./logs",
     "smtp": {
       "enabled": false,
       "host": "your.smtp.server.com",
       "port": 587,
       "user": "username",
       "password": "",
       "from": "alerts@ibis-assistant.com",
       "to": "admin@example.com"
     }
   }
   ```
   
   > **Nota Ticketing**: La configurazione operativa provider-aware è in `config/ticketing_config.json` (template: `config/ticketing_config.example.json`). Le credenziali utente possono essere passate anche via header HTTP per singola request.

### 🧾 Ticketing + PR Automation (Nuovo)

Consulta la guida completa client/operativa:

* **[docs/TICKETING_CLIENT.md](docs/TICKETING_CLIENT.md)**

Include:

* Tool MCP `ticket_*` (search/get/create/update/workflow)
* Tool MCP `repo_pr_*` (create/complete PR Azure DevOps)
* Flusso Dev -> Tester (`resolved` -> `closed`)
* Header runtime per override credenziali provider

4. **Avvia il Server**:
   Il server avvierà automaticamente SurrealDB se configurato per l'uso locale (default). L'avvio standard è:
   
   ```bash
   ./ibis-assistant.exe /run
   ```
   
   Eseguendo `ibis-assistant.exe` senza parametri verrà mostrata la guida rapida ai comandi.

5. **Installazione come Servizio Windows**:
   È possibile installare il server come servizio di sistema per un avvio automatico:

   ```bash
   # Installa il servizio (Richiede privilegi di Amministratore)
   ./ibis-assistant.exe /install

   # Disinstalla il servizio
   ./ibis-assistant.exe /uninstall
   ```
   Il servizio verrà configurato con il nome "ibis-assistant" e descrizione appropriata.

## 🚀 Utilizzo

### Esecuzione del Server e Controllo Demone

Ibis Assistant può essere avviato in modalità interattiva, come servizio Windows, o come demone/processo in background:

*   **`run` (o `-run`)**: Avvia il server interattivamente (comportamento standard MCP).
    ```bash
    ./ibis-assistant.exe run -port 3030 -mode sse
    ```
*   **`start`**: Avvia il server in background come demone. I log del server vengono scritti in `server.log` ed il PID viene salvato in `ibis-assistant.pid`.
    ```bash
    ./ibis-assistant.exe start
    ```
*   **`stop`**: Ferma in modo controllato il server in background (legge il PID da `ibis-assistant.pid` ed arresta il processo).
    ```bash
    ./ibis-assistant.exe stop
    ```
*   **`config`**: Avvia il wizard testuale interattivo per la configurazione iniziale guidata (Fast o Detailed) generandola in `config.json` e `config/ticketing_config.json`.
    ```bash
    ./ibis-assistant.exe config
    ```
*   **`/install` / `/uninstall`**: Installa/disinstalla Ibis Assistant come Servizio Windows permanente (richiede privilegi di amministratore).

> 💡 **Auto-Wizard**: Se `config.json` non è presente all'avvio dell'applicazione, il Wizard interattivo si avvierà automaticamente per guidarti nella configurazione prima di lanciare il server.

### 🖥️ Client CLI (Inoltro Comandi MCP)

L'eseguibile `ibis-assistant` include un client CLI completo che comunica tramite chiamate HTTP REST sicure all'endpoint locale `/api/v1/cli/call`. Questo ti permette di invocare direttamente dal terminale gli strumenti MCP esposti dal server attivo:

*   **Ask (Domande sul Progetto)**:
    ```bash
    ./ibis-assistant.exe ask "Spiega la logica di autenticazione" --branch main
    ```
*   **Ingestion (Codice e Git)**:
    ```bash
    # Indicizza codice locale
    ./ibis-assistant.exe ingest code --path ./percorso/progetto
    # Inizializza sessione Git
    ./ibis-assistant.exe ingest git --name ProgettoA --url https://github.com/org/repo --branch main
    ```
*   **Gestione Ticket**:
    ```bash
    ./ibis-assistant.exe ticket search --query "bug login" --provider jira
    ./ibis-assistant.exe ticket get 1042 --provider redmine
    ./ibis-assistant.exe ticket create --project KEY --title "Nuovo Bug" --desc "Descrizione..."
    ```
*   **Gestione Pull Request**:
    ```bash
    ./ibis-assistant.exe pr create --source feature/nuova --target main --title "Aggiunta feature"
    ```
*   **Analisi Log e Ingestion**:
    ```bash
    ./ibis-assistant.exe logs analyze --project ProgettoA --text "Error: NullReferenceException at..."
    ```
*   **Altro (Memorie, Credenziali, Ottimizzazione)**:
    ```bash
    ./ibis-assistant.exe credentials add --target github.com --token MY_PAT
    ./ibis-assistant.exe memory add --project ProgettoA --text "Usa Go 1.26"
    ./ibis-assistant.exe optimize --iterations 5
    ```

### 🌐 Auto-Discovery HTTP (GET /)

Se si effettua una richiesta GET alla radice del server (es. `http://localhost:3030/`):
- **Web Browser (Accept: text/html)**: Mostra una dashboard web reattiva dal design moderno ed elegante in modalità dark, contenente gli endpoint attivi, le istruzioni di configurazione copia-e-incolla per Claude Desktop, Cursor, Windsurf, e l'elenco interattivo di tutti i tool MCP registrati con relativi schemi di input.
- **Client AI / Programmatico (Accept: application/json)**: Ritorna i metadati strutturati e lo schema JSON completo di tutti i tool esposti, permettendo all'AI di auto-scoprire le potenzialità del server in autonomia senza probing manuale.


### ⚙️ Configurazione e Precedenza
Il server segue una gerarchia di priorità per la configurazione:
1. **Flag da riga di comando** (es. `-port 9000`) - Priorità massima.
2. **Variabili d'ambiente** (es. `PORT=9000`).
3. **File di configurazione** (`config.json`).
4. **Default predefiniti**.

#### 🗄️ Database Auto-Update
Per impostazione predefinita, il server controlla e aggiorna automaticamente l'eseguibile `surreal.exe`. È possibile disabilitare questo comportamento nel `config.json`:
```json
"db_auto_update": false
```
O via variabile d'ambiente: `DB_AUTO_UPDATE=false`.

Il file `config.json` viene cercato automaticamente nella directory corrente e nella directory in cui si trova l'eseguibile (utile quando eseguito come servizio). È possibile specificare un file diverso con il flag `-config <path>`.

#### 🔑 Credenziali Git e PAT (Personal Access Tokens)
Poiché il server può girare come Servizio (es. LocalSystem) o in background, la gestione delle credenziali Git è strutturata su più livelli di priorità:

1. **Override Runtime (Per-User)**: Passato dall'utente tramite gli header HTTP `X-Git-Token` o `X-Git-PAT`. Questo valore ha la priorità massima e viene usato solo per la singola operazione sincrona dell'utente (non memorizzato).
2. **Database Store (Centralizzato)**: Credenziali persistite in modo sicuro sul database SurrealDB (tabella `git_credential`). Possono essere configurate via MCP tool per singoli domini o per specifici URL di repository.
3. **Configurazione Legacy (`config.json`)**: Configurate nel blocco `"git_tokens"` come fallback:
   ```json
     "git_tokens": {"default": ""}
   ```
4. **Variabili d'ambiente**: `GIT_TOKEN=il_tuo_pat` come wildcard globale per tutti gli URL.

##### Configurazione delle credenziali nel database
Gli agenti e i client MCP possono salvare in modo persistente le credenziali sul server tramite lo strumento dedicato:
`git_configure_credentials(target, provider, auth_type, token, username?, ssh_private_key?)`

* **target**: Dominio (es. `github.com`) o l'URL specifico del repository.
* **auth_type**: `token` (per Personal Access Token / PAT), `basic` (username + token/password), o `ssh` (chiave privata).

##### Comportamento al Primo Utilizzo (Unconfigured Repository)
Se viene inizializzato un repository privato (`init_project`) e non vi sono credenziali configurate né a livello di header né sul server, l'operazione fallisce ritornando una risposta strutturata JSON:
```json
{
  "status": "credentials_required",
  "provider": "github",
  "target": "github.com",
  "message": "Git credentials required..."
}
```
L'agente client intercetta questa risposta, richiede il PAT all'utente in modo interattivo e lo memorizza sul server richiamando `git_configure_credentials`.


### 🔌 Integrazione Client (self-hosted)

Ibis Assistant gira in locale (o su un host personale) ed espone un endpoint SSE a cui si collegano i client MCP.

**Endpoint di default:** `http://localhost:3030/sse`

### 🔑 Autenticazione Utente (Ticketing)

Per eseguire azioni che richiedono l'identità dell'utente, il client SSE può passare le credenziali provider via header HTTP (override runtime):

* `X-Redmine-API-Key: <USER_API_KEY>`
* `X-Jira-Email: <USER_EMAIL>`
* `X-Jira-API-Token: <USER_API_TOKEN>`
* `X-Azure-DevOps-PAT: <USER_PAT>`

Se gli header non sono presenti, il server usa le credenziali tecniche definite in `config/ticketing_config.json`.

#### 1. Claude Desktop (Windows/Mac)

Claude Desktop richiede un "bridge" locale per connettersi a server SSE remoti in modo affidabile. Forniamo uno strumento dedicato per questo scopo.

**[👉 Guida all'Integrazione Claude](docs/CLAUDE_INTEGRATION.md)**

Riassunto rapido:
1.  Compila il bridge: `go build -o mcp-bridge.exe ./tools/mcp-bridge`
2.  Configura `%APPDATA%\Claude\claude_desktop_config.json`:
    ```json
    {
      "mcpServers": {
        "knowledge-graph": {
          "command": "C:/path/to/mcp-bridge.exe",
          "args": [
             "-url", "http://localhost:3030/sse",
             "-key", "YOUR_REDMINE_KEY"
          ]
        }
      }
    }
    ```

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

Questo permette di interrogare lo stesso grafo senza reindicizzare i file da ogni client.

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
* **Workflow "Side-by-Side"**: Tenere aperto il **Web Inspector** (vedi punto 3) o **Claude Desktop** accanto all'IDE SQL.
  * *Scenario*: Incollate la definizione di una vista complessa nel Inspector e chiedete: *"Spiegami la logica di business dietro questa vista basandoti sulle Issue Redmine"*.

#### 5. Agenti automatici / CI

Gli agenti possono contattare direttamente l'endpoint locale o remoto.

* **Discovery**: `http://localhost:3030`
* **Vantaggio**: L'agente non deve clonare il repo per capirlo; interroga Ibis Assistant che ha già indicizzato codice e storia.

> **Nota Troubleshooting**: Se il server è su un'altra macchina Windows, aprite la porta **3030** in ingresso sul firewall.



---

## 💡 Casi d'Uso Concreti

Ecco alcuni scenari reali in cui il Ibis Assistant fa la differenza:

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

### 📚 Documentazione Architetturale
Per dettagli tecnici sulle recenti evoluzioni del sistema, consulta:
* **[Stability Refactor 2026](docs/STABILITY_REFACTOR_2026.md)**: Dettagli su Context propagation, Shutdown management e Windows Service optimization.

## 🤖 Istruzioni per AI/Copilot (Sviluppo Server)

Queste istruzioni sono destinate agli agenti AI (come te) che lavorano **sullo sviluppo del Ibis Assistant stesso**.

### Convenzioni Operative
- Trattare `cmd/server/main.go` e `internal/ingest/redmine/ingest.go` come source of truth dei tool MCP.
- Mantenere la retrocompatibilità dei nomi dei tool già pubblici.
- Usare i tool `ticket_*` (i vecchi `redmine_*` sono deprecati/rimossi).
- Le credenziali provider possono essere passate via header runtime (vedi `docs/TICKETING_CLIENT.md`).
- I filtri di ricerca (es. date, sort) devono essere validati rigidamente lato server.

### Convenzioni Architetturali (Maggio 2026)
- **Configurazione**: L'unico file di configurazione tracciato su Git è `config_master.json`. Il file `config.json` locale non deve mai essere committato per evitare leak di chiavi e viene generato dalla build.
- **Parsing AST**: L'estrazione dei chunk e dei log è interamente delegata al Sidecar esterno **ast-grep** (`sg.exe`), garantendo una compilazione 100% pure-Go senza CGO.
- **Regole YAML**: I pattern di estrazione (C#, Go, TS, ecc.) risiedono nella directory `rules/` in formato YAML (gestito da `sgconfig.yml`).
- **Deploy**: Tutto l'ambiente di produzione viene generato in modo automatizzato nella directory `dist/` usando il target `make dist`.

---

*Ibis Assistant — context server Graph + RAG + Timeline (iniziativa "Universal Senior Engineer").*
