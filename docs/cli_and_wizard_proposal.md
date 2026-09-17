# Architecture Proposal: CLI Interface, Background Management, and Configuration Wizard

> [!NOTE]
> **Proposal status: UNDER REVIEW (June 2026)**
> This document describes the architectural proposal for integrating a command-line interface (CLI) in Ibis Assistant for sending MCP commands, a textual assistant (Q&A wizard) for guided first-time product initialization, and an auto-discovery mechanism to ease integration with external AI clients.

---

## 1. Context Analysis and Motivations

Currently, Ibis Assistant primarily operates as a centralized MCP server that waits for connections from external clients (such as Claude Desktop or IDE extensions) over SSE or stdio channels. This mode has several operational limits:
1. **Complex initialization**: The first configuration of `config.json` requires manually filling several complex fields (ports, SurrealDB credentials, Gemini API key, SMTP parameters), raising the barrier to entry for new developers.
2. **No local client**: To query the knowledge base, analyze logs, or interact with tickets from the command line, the user must rely on a graphical MCP client or raw HTTP calls, losing the immediacy of the shell.
3. **No auto-configuration for AI agents**: If an AI (such as Copilot or another local assistant) is instructed to add the Ibis Assistant server by providing only the IP or host (e.g. `http://localhost:3030/`), it has no standard entry point to discover active endpoints (`/sse`, `/mcp`), exposed tools, and how to configure itself to use them.

---

## 2. Configuration Wizard Architecture (`config`)

To improve the onboarding experience (first launch after downloading the executable), an interactive textual console wizard will be introduced.

```
+-------------------------------------------------------------+
|               Starting Ibis Assistant (no config.json)       |
+-------------------------------------------------------------+
                               |
                               v
               +-------------------------------+
               |    Select Mode                |
               |  1) Fast   2) Detailed        |
               +-------------------------------+
                 /                           \
                /                             \
               v                               v
  +--------------------------+    +---------------------------+
  | - Server Port (3030)     |    | - Server Port & Mode      |
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
               |    Write config.json          |
               |    Initialize DB              |
               +-------------------------------+
```

*   **Automatic start**: If the executable is started with no arguments and no `config.json` is found in the current directory or the executable's directory, the server will not exit; it will automatically launch the textual wizard.
*   **Explicit start**: The wizard can be invoked at any time with `ibis-assistant config`.
*   **Fast mode**: Makes the system operational with 5 essential questions, setting defaults for SurrealDB and local Ollama.
*   **Detailed mode**: Allows customizing every option (remote database path, local/remote model settings, default Git credentials, SMTP settings, and Redmine server).

---

## 3. Unified CLI Interface (Client-Server)

To avoid write conflicts and lock issues with the embedded SurrealDB database, the CLI will act as a **thin client** that queries the running local server.

The running server will expose MCP commands. The CLI will interpret terminal subcommands and translate them into MCP Streamable POST calls (`/mcp`) forwarded to the local server:

```
                  +--------------------------+
                  |  Terminal / User         |
                  +--------------------------+
                    | (e.g. ibis-assistant ask "...")
                    v
                  +--------------------------+
                  |  Ibis Assistant CLI (Client) |
                  +--------------------------+
                    | (POST /mcp with JSON-RPC)
                    v
                  +--------------------------+
                  |  Ibis Assistant Server   |
                  +--------------------------+
                    | (Runs ask_project)
                    v
                  +--------------------------+
                  |  SurrealDB / AI Provider |
                  +--------------------------+
```

### Lifecycle Control (Daemon Mode)
To allow the CLI to work smoothly, the server can be managed in the background without a dedicated terminal:
*   `ibis-assistant start` / `ibis-assistant run --daemon`: Starts the executable in the background, writes the PID to `ibis-assistant.pid` in the working directory, redirects standard output to `server.log`, and returns control to the command prompt immediately.
*   `ibis-assistant stop`: Reads the PID from `ibis-assistant.pid`, sends a `SIGTERM` signal for a clean shutdown of the server, log watchers, and embedded database processes, and removes the PID file.

---

## 4. Auto-Discovery Endpoint (`GET /`)

To allow an AI client to integrate autonomously, the server's HTTP root (`/`) will respond based on the request's `Accept` header:

1.  **Web/Markdown request (`Accept: text/*` or browser)**:
    Returns a self-describing HTML or Markdown page that explains to the user (or AI) how to attach to the server:
    *   Active endpoints: SSE (`/sse`) and standard MCP (`/mcp`).
    *   A ready-to-paste JSON configuration block for Claude Desktop (using local `mcp-bridge` or direct SSE).
    *   Instructions for Cursor and Windsurf.
    *   The full list of MCP tools available on the server with their parameters.

2.  **JSON request (`Accept: application/json`)**:
    Returns a structured JSON payload containing metadata for active endpoints and the tool list, allowing an automatic client or AI agent to self-configure by programmatically parsing the response.

---

## 5. Code Change Details

### A. Configuration Wizard (`internal/config/wizard.go`)
This new package will be added to handle interactive terminal input/output:
```go
package config

import (
	"bufio"
	"os"
)

// RunWizard starts the interactive prompt on the terminal
func RunWizard() error {
	scanner := bufio.NewScanner(os.Stdin)
	// Interactive questioning logic...
	return nil
}
```

### B. CLI Client (`internal/cli/client.go`)
This module will translate commands typed at the terminal into POST calls to the server:
```go
package cli

// ExecuteToolCall performs the HTTP call to the local server's /mcp endpoint
func ExecuteToolCall(toolName string, args []string) error {
	// 1. Read the port from config.json
	// 2. Build the JSON-RPC tools/call payload
	// 3. Perform the HTTP POST
	// 4. Print the result
	return nil
}
```

### C. Integration in the Main Switch (`cmd/server/main.go`)
The `main()` function will handle the new logic branches:
```go
func main() {
	// 1. If config.json is missing and there are no special parameters, run the wizard
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
		fmt.Printf("Unknown command: %s\n", cmd)
		printHelp()
	}
}
```
