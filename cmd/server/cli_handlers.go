package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/terenzif/ibis-assistant/internal/config"
)

type mcpServerWrapper struct {
	*server.MCPServer
}

type registeredToolInfo struct {
	Tool    mcp.Tool
	Handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

var cliHandlers = make(map[string]registeredToolInfo)

func (w *mcpServerWrapper) AddTool(tool mcp.Tool, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
	w.MCPServer.AddTool(tool, handler)
	cliHandlers[tool.Name] = registeredToolInfo{
		Tool:    tool,
		Handler: handler,
	}
}

func cliCallHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Metodo non consentito", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Errore decodifica JSON: %v", err), http.StatusBadRequest)
			return
		}

		info, ok := cliHandlers[req.Name]
		if !ok {
			http.Error(w, fmt.Sprintf("Strumento %s non trovato", req.Name), http.StatusNotFound)
			return
		}

		// Eseguiamo il tool
		res, err := info.Handler(r.Context(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      req.Name,
				Arguments: req.Arguments,
			},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	}
}

func autoDiscoveryHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Determina il tipo di risposta basandosi su Accept header
		accept := r.Header.Get("Accept")
		if strings.Contains(accept, "application/json") {
			serveJSONDiscovery(w, r, cfg)
			return
		}

		serveHTMLDiscovery(w, r, cfg)
	}
}

func serveJSONDiscovery(w http.ResponseWriter, r *http.Request, cfg *config.Config) {
	type ToolMeta struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		InputSchema interface{} `json:"input_schema"`
	}

	var tools []ToolMeta
	for _, info := range cliHandlers {
		tools = append(tools, ToolMeta{
			Name:        info.Tool.Name,
			Description: info.Tool.Description,
			InputSchema: info.Tool.InputSchema,
		})
	}
	// Ordina per nome
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	discovery := map[string]interface{}{
		"status":      "online",
		"name":        "Ibis Assistant MCP Server",
		"version":     "1.1.0",
		"environment": "production",
		"endpoints": map[string]string{
			"sse":               "/sse",
			"streamable_http":   "/mcp",
			"cli_bridge_call":   "/api/v1/cli/call",
			"log_push_upload":   "/api/v1/logs/upload",
		},
		"documentation": map[string]interface{}{
			"claude_desktop_config": map[string]interface{}{
				"mcpServers": map[string]interface{}{
					"ibis-assistant": map[string]interface{}{
						"command": "npx",
						"args": []string{
							"-y",
							"@modelcontextprotocol/inspector",
							fmt.Sprintf("http://localhost:%d/sse", cfg.Port),
						},
					},
				},
			},
			"cursor_instructions": fmt.Sprintf("Aggiungi un nuovo MCP Server con tipo 'SSE' ed endpoint 'http://localhost:%d/sse'", cfg.Port),
		},
		"tools": tools,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(discovery)
}

func serveHTMLDiscovery(w http.ResponseWriter, r *http.Request, cfg *config.Config) {
	// Genera la lista dei tool ordinata
	var toolNames []string
	for k := range cliHandlers {
		toolNames = append(toolNames, k)
	}
	sort.Strings(toolNames)

	var toolsHTML strings.Builder
	for _, name := range toolNames {
		info := cliHandlers[name]
		schemaBytes, _ := json.MarshalIndent(info.Tool.InputSchema, "", "  ")
		schemaEscaped := strings.ReplaceAll(string(schemaBytes), "<", "&lt;")
		schemaEscaped = strings.ReplaceAll(schemaEscaped, ">", "&gt;")

		toolsHTML.WriteString(fmt.Sprintf(`
		<div class="tool-item">
			<div class="tool-name">%s</div>
			<div class="tool-desc">%s</div>
			<details class="tool-details">
				<summary>Visualizza Schema Input</summary>
				<pre><code>%s</code></pre>
			</details>
		</div>`, info.Tool.Name, info.Tool.Description, schemaEscaped))
	}

	replacer := strings.NewReplacer(
		"{{PORT}}", fmt.Sprintf("%d", cfg.Port),
		"{{TOOL_COUNT}}", fmt.Sprintf("%d", len(cliHandlers)),
		"{{TOOLS_HTML}}", toolsHTML.String(),
	)

	htmlTemplate := `<!DOCTYPE html>
<html lang="it">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Ibis Assistant - MCP Discovery & Integrazione</title>
    <meta name="description" content="Dashboard di integrazione e auto-discovery per il server MCP Ibis Assistant. Scopri gli endpoint, i tool disponibili e le istruzioni di configurazione.">
    <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600;800&family=JetBrains+Mono:wght@400;700&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg: #0b0f19;
            --surface: rgba(255, 255, 255, 0.03);
            --border: rgba(255, 255, 255, 0.08);
            --text: #f8fafc;
            --text-muted: #94a3b8;
            --primary: #8b5cf6;
            --primary-glow: rgba(139, 92, 246, 0.15);
            --accent: #3b82f6;
            --accent-glow: rgba(59, 130, 246, 0.15);
            --success: #10b981;
        }
        * {
            box-sizing: border-box;
            margin: 0;
            padding: 0;
        }
        body {
            background-color: var(--bg);
            background-image: 
                radial-gradient(circle at 10% 20%, var(--primary-glow) 0%, transparent 40%),
                radial-gradient(circle at 90% 80%, var(--accent-glow) 0%, transparent 40%);
            background-attachment: fixed;
            color: var(--text);
            font-family: 'Outfit', sans-serif;
            line-height: 1.6;
            padding: 3rem 1.5rem;
        }
        .container {
            max-width: 1000px;
            margin: 0 auto;
        }
        header {
            text-align: center;
            margin-bottom: 3rem;
            position: relative;
        }
        h1 {
            font-size: 3rem;
            font-weight: 800;
            background: linear-gradient(135deg, #a78bfa 0%, #60a5fa 100%);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            margin-bottom: 0.5rem;
        }
        .subtitle {
            font-size: 1.2rem;
            color: var(--text-muted);
            margin-bottom: 1rem;
        }
        .status-badge {
            display: inline-flex;
            align-items: center;
            gap: 0.5rem;
            background: rgba(16, 185, 129, 0.1);
            border: 1px solid rgba(16, 185, 129, 0.2);
            color: var(--success);
            padding: 0.4rem 1.2rem;
            border-radius: 9999px;
            font-weight: 600;
            font-size: 0.95rem;
            margin-top: 0.5rem;
        }
        .pulse {
            width: 10px;
            height: 10px;
            background: var(--success);
            border-radius: 50%;
            animation: pulse-animation 2s infinite;
        }
        @keyframes pulse-animation {
            0% { transform: scale(0.95); box-shadow: 0 0 0 0 rgba(16, 185, 129, 0.7); }
            70% { transform: scale(1); box-shadow: 0 0 0 8px rgba(16, 185, 129, 0); }
            100% { transform: scale(0.95); box-shadow: 0 0 0 0 rgba(16, 185, 129, 0); }
        }
        .card {
            background: var(--surface);
            border: 1px solid var(--border);
            backdrop-filter: blur(12px);
            border-radius: 16px;
            padding: 2.2rem;
            margin-bottom: 2rem;
            transition: transform 0.3s ease, border-color 0.3s ease;
        }
        .card:hover {
            border-color: rgba(139, 92, 246, 0.25);
            transform: translateY(-2px);
        }
        h2 {
            font-size: 1.6rem;
            font-weight: 600;
            margin-bottom: 1.5rem;
            display: flex;
            align-items: center;
            gap: 0.6rem;
            color: #e2e8f0;
        }
        h2::before {
            content: '';
            display: inline-block;
            width: 4px;
            height: 1.35rem;
            background: var(--primary);
            border-radius: 2px;
        }
        p {
            color: var(--text-muted);
            margin-bottom: 1.2rem;
            font-size: 1.05rem;
        }
        .grid {
            display: grid;
            grid-template-columns: 1fr 1fr;
            gap: 1.5rem;
        }
        @media (max-width: 768px) {
            .grid { grid-template-columns: 1fr; }
        }
        code, pre {
            font-family: 'JetBrains Mono', monospace;
            font-size: 0.9rem;
        }
        pre {
            background: rgba(0, 0, 0, 0.25);
            padding: 1.2rem;
            border-radius: 8px;
            overflow-x: auto;
            border: 1px solid var(--border);
            margin-bottom: 1rem;
            position: relative;
        }
        .copy-btn {
            position: absolute;
            top: 0.5rem;
            right: 0.5rem;
            background: rgba(255, 255, 255, 0.05);
            border: 1px solid var(--border);
            color: var(--text-muted);
            padding: 0.3rem 0.7rem;
            border-radius: 4px;
            cursor: pointer;
            font-size: 0.8rem;
            font-family: inherit;
            transition: all 0.2s;
        }
        .copy-btn:hover {
            background: var(--primary);
            color: #fff;
            border-color: var(--primary);
        }
        .tabs {
            display: flex;
            gap: 0.5rem;
            margin-bottom: 1.2rem;
            border-bottom: 1px solid var(--border);
            padding-bottom: 0.5rem;
        }
        .tab-btn {
            background: none;
            border: none;
            color: var(--text-muted);
            padding: 0.6rem 1.2rem;
            cursor: pointer;
            font-family: inherit;
            font-size: 0.95rem;
            font-weight: 600;
            border-radius: 6px;
            transition: all 0.2s;
        }
        .tab-btn:hover {
            color: var(--text);
        }
        .tab-btn.active {
            background: rgba(139, 92, 246, 0.1);
            color: var(--primary);
        }
        .tab-content {
            display: none;
        }
        .tab-content.active {
            display: block;
        }
        .tool-list {
            display: flex;
            flex-direction: column;
            gap: 1.2rem;
        }
        .tool-item {
            background: rgba(0, 0, 0, 0.15);
            border: 1px solid var(--border);
            border-radius: 10px;
            padding: 1.2rem;
        }
        .tool-name {
            font-family: 'JetBrains Mono', monospace;
            font-weight: 700;
            color: #a78bfa;
            font-size: 1.1rem;
            margin-bottom: 0.4rem;
        }
        .tool-desc {
            font-size: 1rem;
            color: var(--text-muted);
            margin-bottom: 0.8rem;
        }
        .tool-details summary {
            font-size: 0.9rem;
            color: var(--accent);
            cursor: pointer;
            outline: none;
            user-select: none;
        }
        .tool-details summary:hover {
            color: #60a5fa;
        }
        .tool-details[open] summary {
            margin-bottom: 0.8rem;
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>Ibis Assistant</h1>
            <div class="subtitle">Discovery &amp; Integrazione Server MCP</div>
            <div class="status-badge" id="statusBadge">
                <span class="pulse"></span>
                Server Online (Porta {{PORT}})
            </div>
        </header>

        <div class="grid">
            <div class="card">
                <h2>Endpoint Attivi</h2>
                <p>Ibis Assistant espone due canali standard per la comunicazione MCP (Model Context Protocol) e due endpoint interni per le utilità:</p>
                <div style="display: flex; flex-direction: column; gap: 0.8rem;">
                    <div>
                        <strong>SSE Endpoint:</strong> 
                        <pre style="padding: 0.5rem; margin: 0.25rem 0;"><code>http://localhost:{{PORT}}/sse</code></pre>
                    </div>
                    <div>
                        <strong>Streamable HTTP (Standard):</strong> 
                        <pre style="padding: 0.5rem; margin: 0.25rem 0;"><code>http://localhost:{{PORT}}/mcp</code></pre>
                    </div>
                    <div>
                        <strong>CLI Bridge Endpoint:</strong> 
                        <pre style="padding: 0.5rem; margin: 0.25rem 0;"><code>http://localhost:{{PORT}}/api/v1/cli/call</code></pre>
                    </div>
                </div>
            </div>

            <div class="card">
                <h2>Integrazione IDE / Client</h2>
                <div class="tabs">
                    <button class="tab-btn active" onclick="switchTab('claude')">Claude Desktop</button>
                    <button class="tab-btn" onclick="switchTab('cursor')">Cursor / Windsurf</button>
                </div>
                
                <div id="tab-claude" class="tab-content active">
                    <p>Inserisci questa configurazione nel tuo file <code>claude_desktop_config.json</code>:</p>
                    <pre><button class="copy-btn" onclick="copyText('claudeConfig')">Copia</button><code id="claudeConfig">{
  "mcpServers": {
    "ibis-assistant": {
      "command": "npx",
      "args": [
        "-y",
        "@modelcontextprotocol/inspector",
        "http://localhost:{{PORT}}/sse"
      ]
    }
  }
}</code></pre>
                </div>
                
                <div id="tab-cursor" class="tab-content">
                    <p>Per integrare Ibis Assistant in Cursor o Windsurf:</p>
                    <ol style="margin-left: 1.5rem; color: var(--text-muted); display: flex; flex-direction: column; gap: 0.5rem;">
                        <li>Apri le impostazioni del tuo editor (Settings &gt; Features &gt; MCP).</li>
                        <li>Aggiungi un nuovo server chiamandolo <strong>Ibis Assistant</strong>.</li>
                        <li>Imposta il tipo di trasporto su <strong>SSE</strong>.</li>
                        <li>Usa l'URL dell'endpoint: <code style="color: var(--accent);">http://localhost:{{PORT}}/sse</code></li>
                    </ol>
                </div>
            </div>
        </div>

        <div class="card">
            <h2>Comandi CLI Locali</h2>
            <p>Puoi interagire con il server direttamente dalla tua shell locale lanciando comandi sull'eseguibile:</p>
            <pre><code># Fai domande sul codice del progetto indicizzato
ibis-assistant ask "Spiega la logica di autenticazione" --branch main

# Indicizza una cartella di codice locale
ibis-assistant ingest code --path ./percorso/progetto

# Cerca ticket associati su Redmine
ibis-assistant ticket search --query "login" --status open</code></pre>
        </div>

        <div class="card">
            <h2>Strumenti MCP Disponibili ({{TOOL_COUNT}})</h2>
            <div class="tool-list">
                {{TOOLS_HTML}}
            </div>
        </div>
    </div>

    <script>
        function switchTab(tabId) {
            document.querySelectorAll('.tab-btn').forEach(btn => btn.classList.remove('active'));
            document.querySelectorAll('.tab-content').forEach(content => content.classList.remove('active'));
            
            event.target.classList.add('active');
            document.getElementById('tab-' + tabId).classList.add('active');
        }

        function copyText(elementId) {
            var text = document.getElementById(elementId).innerText;
            navigator.clipboard.writeText(text).then(() => {
                const btn = event.target;
                const originalText = btn.innerText;
                btn.innerText = 'Copiato!';
                btn.style.backgroundColor = 'var(--success)';
                setTimeout(() => {
                    btn.innerText = originalText;
                    btn.style.backgroundColor = '';
                }, 2000);
            });
        }
    </script>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(replacer.Replace(htmlTemplate)))
}
