# Phased Implementation Guide: CLI Interface, Background Management, and Configuration Wizard

> [!NOTE]
> **Implementation status: COMPLETED** (commit `b139255`)
> All phases have been implemented and verified with green tests. This document is kept as an architectural reference.
>
> **Residual risks identified (post-naming analysis):**
> - PID/log path portability when the executable starts from a non-standard directory (e.g. `System32` in Windows service mode).
> - Behavior parity between service mode (`/run`) and interactive run for shutdown hooks.
> - CLI UX error handling when the server is unreachable: currently the error is a generic HTTP error.

---

## Phase Overview
* **Phase 1:** Interactive Configuration Wizard (`internal/config/wizard.go`)
* **Phase 2:** HTTP CLI Client for MCP Tools (`internal/cli/client.go`)
* **Phase 3:** Daemon Management & Lifecycle Control (`start` / `stop` via PID file)
* **Phase 4:** Routing and Integration in the Main File (`cmd/server/main.go`)
* **Phase 5:** Auto-Discovery Endpoint (`GET /`)
* **Phase 6:** Validation Tests

---

## Phase 1: Interactive Configuration Wizard
**Goal:** Create an interactive console interface that guides the user in creating `config.json`.

### Operational steps:
1. Create the file `internal/config/wizard.go`.
2. Implement `RunWizard()` that reads from `stdin` (using `bufio.NewScanner(os.Stdin)`):
   * Ask for mode choice: `1) Fast` or `2) Detailed`.
   * **In Fast mode**:
     * Server port (default: `3030`).
     * Enable AI Reasoning (Gemini) (Y/N, default Y).
     * If enabled, ask for the API key (check whether `GEMINI_API_KEY` is present in the environment and propose it as default).
     * Discovery Root folder (default: `./repos`).
     * Logs Root folder (default: `./logs`).
   * **In Detailed mode**:
     * Port (default: `3030`) and Mode (`sse` / `stdio`, default: `sse`).
     * Database: Choice between `1) Embedded SurrealDB` or `2) Remote SurrealDB`.
       * If Embedded: Data Path (default: `./db`), Auto-update (Y/N, default: Y).
       * If Remote: URL (default: `ws://localhost:8000/rpc`), Namespace, Database, User, Password.
     * AI Embedding: Provider (`ollama` or `gemini`). If Ollama, ask for URL, Model (default `nomic-embed-text`), Auto-start (Y/N, default Y), Auto-update (Y/N, default Y).
     * AI Reasoning: Provider (`gemini` or `none`). If Gemini, ask for Gemini keys (supports multiple comma-separated input) and default RPM.
     * Discovery Root and Auto-scan (Y/N).
     * Log ingestion: Logs Root, HTTP log ingestion (Y/N). If enabled, ask for or auto-generate an API Key.
     * SMTP: Enable and optional credentials/host (if enabled).
     * Ticketing: Configure Redmine (Y/N). If yes, ask for Redmine URL and Redmine API Key.
3. Serialize the resulting configuration into a structured `config.json` file. If Redmine is configured, also generate `config/ticketing_config.json`.

---

## Phase 2: HTTP CLI Client for MCP Tools
**Goal:** Allow the CLI to send commands to the server and format the results.

### Operational steps:
1. Create the file `internal/cli/client.go`.
2. Implement `ExecuteToolCall(cmd string, args []string)`:
   * Read `config.json` to obtain the server port and address (default: `http://localhost:3030`).
   * Build the MCP JSON-RPC request for the tool call (e.g. `{ "jsonrpc": "2.0", "method": "tools/call", "params": { "name": "...", "arguments": { ... } }, "id": 1 }`).
   * Send the request to the `/mcp` or `/mcp/` endpoint via HTTP POST.
   * Receive the JSON response and format it for the console:
     * If the result is plain text, print it.
     * If it is structured JSON, format it with indentation.
     * If it is an error, print it to `os.Stderr` and exit with a non-zero code.

---

## Phase 3: Daemon Management & Lifecycle Control
**Goal:** Allow the server to run in the background in daemon mode and be controlled from the terminal via a PID file.

### Operational steps:
1. Implement background start logic in `main.go`:
   * When `start` or `run --daemon` is launched:
     * Check whether `ibis-assistant.pid` already exists and is associated with an active process (if so, fail indicating the server is already running).
     * Execute the same executable (`os.Executable()`) with the run arguments (e.g. `run`) and redirect `stdout` and `stderr` to a log file (e.g. `server.log`).
     * Write the child process PID to `ibis-assistant.pid` in the executable directory or the current directory.
     * Print a message such as `Server started in background with PID <PID>` and exit immediately, returning control to the shell.
2. Implement stop logic:
   * When `stop` is launched:
     * Read the PID from `ibis-assistant.pid`.
     * If the file does not exist, print an error (`Server not running`).
     * Find the process (`os.FindProcess(pid)`) and send a controlled termination signal (`syscall.SIGTERM` or `os.Interrupt` on Windows).
     * Wait briefly to verify the process has stopped.
     * Remove `ibis-assistant.pid`.
     * Print `Server stopped successfully`.

---

## Phase 4: Routing and Integration in the Main File
**Goal:** Hook the new flows into the CLI option switch in `cmd/server/main.go`.

### Operational steps:
1. In `cmd/server/main.go`, modify `main()`:
   * **Configuration check**: If configuration does not exist, automatically launch `wizard.RunWizard()`.
   * **Command switch**:
     * Add `case "config":` to launch the wizard manually.
     * Add `case "start", "run --daemon":` for background bootstrap.
     * Add `case "stop":` for controlled termination.
     * Add cases for CLI commands (`ask`, `ticket`, `pr`, `ingest`, `logs`, `credentials`, `memory`, `outcome`, `optimize`). Defer call handling to `internal/cli.ExecuteToolCall()`.
2. Extend `printHelp()` to document the syntax of all CLI commands and daemon control commands in English.

---

## Phase 5: Auto-Discovery Endpoint (`GET /`)
**Goal:** Implement the informational endpoint on the server `/` route to ease integration with external AI clients.

### Operational steps:
1. Register an HTTP handler on the `/` route in `cmd/server/main.go`:
   ```go
   mux.HandleFunc("/", autoDiscoveryHandler(cfg, s))
   ```
2. The function must respond by distinguishing `Accept` headers:
   * **`Accept: application/json`**:
     Returns structured JSON with server metadata:
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
   * **Other (Browser / Web Client)**:
     Returns a self-describing HTML or Markdown page that:
     * Explains the usefulness of the Ibis Assistant server.
     * Shows ready-to-use JSON configuration snippets for Claude Desktop, Cursor, and Windsurf.
     * Lists CLI commands and MCP tools enabled in the system.

---

## Phase 6: Validation Tests
**Goal:** Write unit tests to validate CLI parsing and the wizard write path.

### Operational steps:
1. Create `internal/cli/client_test.go` to test correct construction of JSON-RPC payloads from command-line parameters.
2. Run a build with `go build` to verify there are no syntax errors.
3. Run the full test suite with `go test ./...` to ensure the changes have not introduced regressions.
