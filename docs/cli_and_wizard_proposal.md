# Proposta di Architettura: Interfaccia CLI, Gestione Background e Wizard di Configurazione

> [!NOTE]
> **Stato della proposta: IN VALUTAZIONE (Giugno 2026)**
> Questo documento descrive la proposta architetturale per l'integrazione di un'interfaccia a riga di comando (CLI) in `ibis-assistant` per l'invio dei comandi MCP, di un assistente testuale (wizard Q&A) per la prima inizializzazione guidata del prodotto e di un meccanismo di auto-discovery per facilitare l'integrazione dei client AI esterni.

---

## 1. Analisi del Contesto e Motivazioni

Attualmente, `ibis-assistant` funziona principalmente come un server MCP centralizzato che attende connessioni da client esterni (quali Claude Desktop o estensioni IDE) tramite canali SSE o stdio. Questa modalità presenta alcuni limiti operativi:
1. **Inizializzazione complessa**: La prima configurazione del file `config.json` richiede la compilazione manuale di diversi campi complessi (porte, credenziali DB SurrealDB, API Key Gemini, parametri SMTP), aumentando la barriera d'ingresso per i nuovi sviluppatori.
2. **Assenza di un client locale**: Per interrogare la base di conoscenza, analizzare i log o interagire con i ticket da riga di comando, l'utente è costretto a ricorrere a un client grafico MCP o a effettuare chiamate HTTP raw, perdendo l'immediatezza della shell.
3. **Mancanza di auto-configurazione per gli agenti AI**: Se un'IA (come Copilot o un altro assistente locale) viene istruita ad aggiungere il server `ibis-assistant` indicando solo l'IP o l'host (es. `http://localhost:3030/`), non ha un punto di ingresso standard per scoprire gli endpoint attivi (`/sse`, `/mcp`), i tool esposti e come configurare se stessa per usarli.

---

## 2. Architettura del Wizard di Configurazione (`config`)

Per migliorare l'esperienza di onboarding (primo avvio dopo il download dell'eseguibile), verrà introdotto un wizard testuale interattivo guidato da console.

```
+-------------------------------------------------------------+
|               Avvio di ibis-assistant (senza config.json)          |
+-------------------------------------------------------------+
                               |
                               v
               +-------------------------------+
               |    Seleziona Modalità         |
               |  1) Veloce   2) Dettagliata   |
               +-------------------------------+
                 /                           \
                /                             \
               v                               v
  +--------------------------+    +---------------------------+
  | - Porta Server (3030)    |    | - Server Port & Mode      |
  | - AI Reasoning (Gemini)  |    | - Database Type & Creds   |
  | - Gemini API Key         |    | - AI Embedding & Models   |
  | - Discovery Root         |    | - AI Reasoning & Keys     |
  | - Logs Root              |    | - SMTP Settings           |
  |                          |    | - Ticketing (Redmine)     |
  +--------------------------+    +---------------------------+
                \                             /
                 \                           /
                  v                         v
               +-------------------------------+
               |    Scrittura config.json      |
               |    Inizializzazione DB        |
               +-------------------------------+
```

*   **Avvio Automatico**: Se l'eseguibile viene avviato senza argomenti e non viene individuato alcun file `config.json` nella directory corrente o in quella dell'eseguibile, il server non si arresterà, ma lancerà automaticamente il wizard testuale.
*   **Avvio Esplicito**: Il wizard potrà essere invocato in qualsiasi momento lanciando il comando `ibis-assistant config`.
*   **Modalità Veloce (Fast)**: Consente di rendere operativo il sistema in 5 domande essenziali, impostando i valori di default per SurrealDB ed Ollama locale.
*   **Modalità Dettagliata (Detailed)**: Consente di personalizzare ogni singola opzione (percorso del database remoto, impostazioni dei modelli locali/remoti, credenziali Git di default, configurazioni SMTP e server Redmine).

---

## 3. Interfaccia CLI Unificata (Client-Server)

Per evitare conflitti di scrittura e problemi legati al lock del database SurrealDB integrato, la CLI funzionerà come un **client sottile** che interroga il server locale attivo.

Il server in esecuzione esporrà i comandi MCP. La CLI interpreterà i sotto-comandi da terminale e li tradurrà in chiamate MCP Streamable POST (`/mcp`) inoltrate al server locale:

```
                  +--------------------------+
                  |  Terminale / Utente      |
                  +--------------------------+
                    | (es. ibis-assistant ask "...")
                    v
                  +--------------------------+
                  |  ibis-assistant CLI (Client)   |
                  +--------------------------+
                    | (POST /mcp con JSON-RPC)
                    v
                  +--------------------------+
                  |  ibis-assistant Server         |
                  +--------------------------+
                    | (Esegue ask_project)
                    v
                  +--------------------------+
                  |  SurrealDB / AI Provider |
                  +--------------------------+
```

### Controllo Ciclo di Vita (Daemon Mode)
Per consentire alla CLI di funzionare in modo agevole, il server potrà essere gestito in background senza richiedere un terminale dedicato:
*   `ibis-assistant start` / `ibis-assistant run --daemon`: Avvia l'eseguibile in background, scrive il PID nel file `ibis-assistant.pid` nella directory di lavoro, devia lo standard output nel file `server.log` e ritorna immediatamente il controllo al prompt dei comandi.
*   `ibis-assistant stop`: Legge il PID da `ibis-assistant.pid`, invia un segnale `SIGTERM` per arrestare in modo pulito il server, i log watcher e i processi embedded del database, e rimuove il file PID.

---

## 4. Endpoint di Auto-Discovery (`GET /`)

Per consentire a un'IA client di integrarsi in autonomia, la radice HTTP (`/`) del server risponderà in base all'header `Accept` della richiesta:

1.  **Richiesta Web/Markdown (`Accept: text/*` o browser)**:
    Restituisce una pagina HTML o Markdown autodescrittiva che spiega all'utente (o all'IA) come agganciare il server:
    *   Gli endpoint attivi: SSE (`/sse`) e standard MCP (`/mcp`).
    *   Un blocco di configurazione JSON copia-incolla già pronto per Claude Desktop (con uso di `mcp-bridge` locale o SSE diretto).
    *   Istruzioni per Cursor e Windsurf.
    *   L'elenco completo dei tool MCP disponibili sul server con i relativi parametri.

2.  **Richiesta JSON (`Accept: application/json`)**:
    Restituisce un payload JSON strutturato contenente i metadati degli endpoint attivi e la lista dei tool, consentendo a un client automatico o a un agente AI di autoconfigurarsi effettuando il parsing programmatico della risposta.

---

## 5. Dettaglio delle Modifiche al Codice

### A. Wizard di Configurazione (`internal/config/wizard.go`)
Verrà aggiunto questo nuovo package per gestire l'input/output interattivo da terminale:
```go
package config

import (
	"bufio"
	"os"
)

// RunWizard avvia il prompt interattivo sul terminale
func RunWizard() error {
	scanner := bufio.NewScanner(os.Stdin)
	// Logica di interrogazione interattiva...
	return nil
}
```

### B. Client CLI (`internal/cli/client.go`)
Questo modulo gestirà la traduzione dei comandi digitati a terminale in chiamate POST sul server:
```go
package cli

// ExecuteToolCall esegue la chiamata HTTP verso l'endpoint /mcp del server locale
func ExecuteToolCall(toolName string, args []string) error {
	// 1. Legge la porta da config.json
	// 2. Costruisce il payload JSON-RPC tools/call
	// 3. Esegue la POST HTTP
	// 4. Stampa il risultato
	return nil
}
```

### C. Integrazione nel Switch Principale (`cmd/server/main.go`)
La funzione `main()` gestirà i nuovi rami logici:
```go
func main() {
	// 1. Se config.json non è presente e non ci sono parametri speciali, esegui il wizard
	if !configExists() && len(os.Args) < 2 {
		config.RunWizard()
	}

	if len(os.Args) < 2 {
		printHelp()
		return
	}

	cmd := os.Args[1]
	switch cmd {
	case "config":
		config.RunWizard()
	case "start":
		startBackgroundServer()
	case "stop":
		stopBackgroundServer()
	case "run":
		runServer(context.Background())
	// CLI subcommands
	case "ask", "ticket", "pr", "ingest", "logs":
		cli.ExecuteToolCall(cmd, os.Args[2:])
	default:
		fmt.Printf("Comando sconosciuto: %s\n", cmd)
		printHelp()
	}
}
```
