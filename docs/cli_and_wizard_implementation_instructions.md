# Guida Operativa in Fasi: Interfaccia CLI, Gestione Background e Wizard di Configurazione

> [!NOTE]
> **Stato dell'implementazione: PIANIFICATO**
> Questa guida operativa definisce le specifiche di dettaglio per l'implementazione incrementale del wizard di configurazione (veloce/dettagliato), dei comandi di controllo del demone (`start`/`stop`), del client CLI per i tool MCP, e dell'endpoint di auto-discovery sulla radice del server.

---

## Panoramica delle Fasi
* **Fase 1:** Wizard di Configurazione Interattivo (`internal/config/wizard.go`)
* **Fase 2:** Client HTTP CLI per i Tool MCP (`internal/cli/client.go`)
* **Fase 3:** Gestione Demone & Controllo Ciclo di Vita (`start` / `stop` via PID file)
* **Fase 4:** Routing e Integrazione nel file Principale (`cmd/server/main.go`)
* **Fase 5:** Endpoint di Auto-Discovery (`GET /`)
* **Fase 6:** Test di Validazione

---

## Fase 1: Wizard di Configurazione Interattivo
**Obiettivo:** Creare un'interfaccia interattiva da console che guidi l'utente nella creazione del file `config.json`.

### Passi operativi:
1. Crea il file `internal/config/wizard.go`.
2. Implementa la funzione `RunWizard()` che legge da `stdin` (utilizzando `bufio.NewScanner(os.Stdin)`):
   * Chiedi la scelta della modalità: `1) Veloce (Fast)` o `2) Dettagliata (Detailed)`.
   * **In modalità Veloce**:
     * Porta del server (default: `3030`).
     * Abilitazione AI Reasoning (Gemini) (S/N, default S).
     * Se abilitata, chiedi la chiave API (controlla se `GEMINI_API_KEY` è presente in ambiente e proponila come default).
     * Cartella Discovery Root (default: `./repos`).
     * Cartella Logs Root (default: `./logs`).
   * **In modalità Dettagliata**:
     * Porta (default: `3030`) e Modalità (`sse` / `stdio`, default: `sse`).
     * Database: Scelta tra `1) Embedded SurrealDB` o `2) Remote SurrealDB`.
       * Se Embedded: Data Path (default: `./db`), Auto-update (S/N, default: S).
       * Se Remote: URL (default: `ws://localhost:8000/rpc`), Namespace, Database, User, Password.
     * AI Embedding: Provider (`ollama` o `gemini`). Se Ollama, chiedi URL, Modello (default `nomic-embed-text`), Auto-start (S/N, default S), Auto-update (S/N, default S).
     * AI Reasoning: Provider (`gemini` o `none`). Se Gemini, chiedi le chiavi Gemini (supporta input multiplo separato da virgola) e RPM di default.
     * Discovery Root e Auto-scan (S/N).
     * Ingestion Log: Logs Root, HTTP log ingestion (S/N). Se abilitata, chiedi o autogenera API Key.
     * SMTP: Abilitazione ed eventuali credenziali/host (se abilitato).
     * Ticketing: Configura Redmine (S/N). Se sì, chiedi Redmine URL e Redmine API Key.
3. Serializza la configurazione risultante in un file `config.json` strutturato. Se configurato Redmine, genera anche `config/ticketing_config.json`.

---

## Fase 2: Client HTTP CLI per i Tool MCP
**Obiettivo:** Permettere alla CLI di inviare i comandi al server e formattare i risultati.

### Passi operativi:
1. Crea il file `internal/cli/client.go`.
2. Implementa la funzione `ExecuteToolCall(cmd string, args []string)`:
   * Leggi il file `config.json` per ricavare la porta e l'indirizzo del server (default: `http://localhost:3030`).
   * Costruisci la richiesta MCP JSON-RPC per la chiamata del tool (es. `{ "jsonrpc": "2.0", "method": "tools/call", "params": { "name": "...", "arguments": { ... } }, "id": 1 }`).
   * Invia la richiesta all'endpoint `/mcp` o `/mcp/` tramite una richiesta HTTP POST.
   * Ricevi la risposta JSON e formattala a console:
     * Se il risultato è testo normale, stampalo.
     * Se è JSON strutturato, formattalo con indentazione.
     * Se è un errore, stampalo su `os.Stderr` ed esci con codice non zero.

---

## Fase 3: Gestione Demone & Controllo Ciclo di Vita
**Obiettivo:** Permettere al server di girare in background in modalità demone ed essere controllato da terminale tramite file PID.

### Passi operativi:
1. Implementa in `main.go` la logica di avvio in background:
   * Quando viene lanciato il comando `start` o `run --daemon`:
     * Controlla se `ibis-assistant.pid` esiste già ed è associato a un processo attivo (se sì, fallisci indicando che il server è già in esecuzione).
     * Esegui l'eseguibile stesso (`os.Executable()`) passando gli argomenti di esecuzione (es. `run`) e reindirizzando `stdout` e `stderr` a un file di log (es. `server.log`).
     * Scrivi il PID del nuovo processo figlio nel file `ibis-assistant.pid` nella directory dell'eseguibile o in quella corrente.
     * Stampa un messaggio del tipo `Server avviato in background con PID <PID>` ed esci immediatamente ritornando il controllo alla shell.
2. Implementa la logica di stop:
   * Quando viene lanciato il comando `stop`:
     * Leggi il PID dal file `ibis-assistant.pid`.
     * Se il file non esiste, stampa un errore (`Server non in esecuzione`).
     * Trova il processo (`os.FindProcess(pid)`) e invia un segnale di terminazione controllata (`syscall.SIGTERM` o `os.Interrupt` su Windows).
     * Attendi brevemente per verificare che il processo si sia arrestato.
     * Rimuovi il file `ibis-assistant.pid`.
     * Stampa `Server arrestato correttamente`.

---

## Fase 4: Routing e Integrazione nel file Principale
**Obiettivo:** Agganciare i nuovi flussi al switch delle opzioni CLI in `cmd/server/main.go`.

### Passi operativi:
1. In `cmd/server/main.go`, modifica la funzione `main()`:
   * **Controllo Configurazione**: Se la configurazione non esiste, lancia automaticamente il wizard `wizard.RunWizard()`.
   * **Switch dei Comandi**:
     * Aggiungi `case "config":` per lanciare il wizard manualmente.
     * Aggiungi `case "start", "run --daemon":` per il bootstrap in background.
     * Aggiungi `case "stop":` per la terminazione controllata.
     * Aggiungi i case per i comandi CLI (`ask`, `ticket`, `pr`, `ingest`, `logs`, `credentials`, `memory`, `outcome`, `optimize`). Deferisci la gestione della chiamata a `internal/cli.ExecuteToolCall()`.
2. Estendi `printHelp()` per documentare in italiano la sintassi di tutti i comandi CLI e i comandi di controllo del demone.

---

## Fase 5: Endpoint di Auto-Discovery (`GET /`)
**Obiettivo:** Implementare l'endpoint informativo sulla rotta `/` del server per agevolare l'integrazione con client AI esterni.

### Passi operativi:
1. Registra un handler HTTP sulla rotta `/` in `cmd/server/main.go`:
   ```go
   mux.HandleFunc("/", autoDiscoveryHandler(cfg, s))
   ```
2. La funzione deve rispondere distinguendo gli header `Accept`:
   * **`Accept: application/json`**:
     Restituisce un JSON strutturato con i metadati del server:
     ```json
     {
       "status": "online",
       "endpoints": {
         "sse": "/sse",
         "mcp": "/mcp"
       },
       "version": "1.1.0",
       "tools": [ ... ]
     }
     ```
   * **Altro (Browser / Client Web)**:
     Restituisce una pagina HTML o Markdown autodescrittiva che:
     * Spiega l'utilità del server Ibis Assistant.
     * Mostra frammenti di configurazione JSON pronti per l'integrazione in Claude Desktop, Cursor e Windsurf.
     * Elenca i comandi della CLI e i tool MCP abilitati nel sistema.

---

## Fase 6: Test di Validazione
**Obiettivo:** Scrivere test unitari per validare il parsing CLI e il wizard di scrittura.

### Passi operativi:
1. Crea `internal/cli/client_test.go` per testare la corretta costruzione dei JSON RPC payload a partire dai parametri passati da riga di comando.
2. Esegui la compilazione con `go build` per verificare che non ci siano errori di sintassi.
3. Esegui la suite di test completa tramite `go test ./...` per assicurare che le modifiche non abbiano introdotto regressioni.
