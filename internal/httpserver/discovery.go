package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/terenzif/ibis-assistant/internal/config"
)

const GuideURI = "ibis://guide"

type toolMeta struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

func listToolMeta(mcpServer *server.MCPServer) []toolMeta {
	var tools []toolMeta
	if mcpServer == nil {
		return tools
	}
	for _, st := range mcpServer.ListTools() {
		if st == nil {
			continue
		}
		tools = append(tools, toolMeta{
			Name:        st.Tool.Name,
			Description: st.Tool.Description,
			InputSchema: st.Tool.InputSchema,
		})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

func protocolVersions() []string {
	return append([]string(nil), mcp.ValidProtocolVersions...)
}

// GuideMarkdown is the human install/usage guide (also served as MCP resource).
func GuideMarkdown(cfg *config.Config, tools []toolMeta) string {
	base := cfg.PublicBaseURL()
	mode := strings.TrimSpace(cfg.RuntimeMode)
	if mode == "" {
		mode = "(unset — listener binds all interfaces)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Ibis Assistant\n\n")
	fmt.Fprintf(&b, "Living project memory over MCP. One binary: personal, server, or Cursor plugin (stdio child).\n\n")
	fmt.Fprintf(&b, "- **Runtime:** `%s`\n", mode)
	fmt.Fprintf(&b, "- **Listen:** `%s`\n", cfg.ListenAddr())
	fmt.Fprintf(&b, "- **Streamable HTTP (default):** `%s/mcp`\n", base)
	fmt.Fprintf(&b, "- **Legacy SSE:** `%s/sse` (deprecated; use `/mcp`)\n", base)
	fmt.Fprintf(&b, "- **Human/agent guide:** `GET %s/` (`Accept: text/html` | `application/json` | `text/markdown`) and MCP resource `%s`\n\n", base, GuideURI)

	b.WriteString("## Install\n\n")
	b.WriteString("1. Install Go 1.27+ (this repo pins `toolchain go1.27.1`).\n")
	b.WriteString("2. `go build -o ibis-assistant ./cmd/server` (Windows: `ibis-assistant.exe`).\n")
	b.WriteString("3. Copy `config_master.json` to `config.json` (never commit `config.json`).\n")
	b.WriteString("4. Set `runtime_mode` to `personal` (same PC as your repos) or `server` (dedicated host).\n")
	b.WriteString("5. For personal mode, set `projects[].working_repo_path` (or `git_repos`) to the live checkout. Do not point `discovery_root/dynamic` at a developer tree.\n")
	b.WriteString("6. Run `ibis-assistant run`, or `start`/`stop` for a daemon, or `/install` as a Windows service. Cursor plugin: `ibis-assistant -mode stdio` with `RUNTIME_MODE=plugin`.\n\n")
	b.WriteString("Personal mode binds `127.0.0.1` unless `bind_address` is set. Server mode binds `0.0.0.0`.\n\n")

	b.WriteString("## Agent autoconfig\n\n")
	b.WriteString("HTTP clients: `GET /` with `Accept: application/json`. MCP 2026-07-28 clients: `server/discover` on `/mcp` (legacy clients still `initialize`). Then `resources/read` on `ibis://guide` to show this page to a human.\n\n")
	b.WriteString("Cursor / VS Code MCP (Streamable HTTP):\n\n")
	b.WriteString("```json\n")
	fmt.Fprintf(&b, "{\n  \"mcpServers\": {\n    \"ibis-assistant\": {\n      \"url\": \"%s/mcp\"\n    }\n  }\n}\n", base)
	b.WriteString("```\n")
	b.WriteString("\nClaude Desktop still uses `mcp-bridge` against legacy SSE until that bridge is updated.\n\n")
	b.WriteString("Inspector: `npx @modelcontextprotocol/inspector " + base + "/mcp`\n\n")

	b.WriteString("## CLI\n\n")
	b.WriteString("The same binary is an MCP client of a running server (`POST /mcp`):\n\n")
	b.WriteString("```bash\n")
	b.WriteString("ibis-assistant ask \"Explain the auth flow\" --branch main\n")
	b.WriteString("ibis-assistant ingest code --path ./path/to/project\n")
	b.WriteString("ibis-assistant logs analyze --project MyApp --text \"ERROR timeout\"\n")
	b.WriteString("```\n\n")

	b.WriteString("## Main tools\n\n")
	b.WriteString("- `init_project` — sync git workspace and **wait** for ingest (progress notifications if the client sends `progressToken`).\n")
	b.WriteString("- `ask_project` — hybrid search over the knowledge graph.\n")
	b.WriteString("- `analyze_logs` — synchronous log analysis.\n")
	b.WriteString("- `ingest_code` / `ingest` CLI — vectorize a tree.\n")
	b.WriteString("- `sync_local_patch` — server clones only, when `init_project` returns `requires_patch`.\n\n")

	b.WriteString("### Auth headers (optional per request)\n\n")
	b.WriteString("`X-Git-Token` / `X-Git-PAT`, `X-Redmine-API-Key`, `X-Jira-Email` + `X-Jira-API-Token`, `X-Azure-DevOps-PAT`.\n\n")

	if cfg.LogIngestion.HTTP.Enabled {
		b.WriteString("Log HTTP upload is enabled at `POST /api/v1/logs/upload` (`X-API-Key`).\n\n")
	}

	if len(tools) > 0 {
		b.WriteString("## Registered MCP tools\n\n")
		for _, t := range tools {
			fmt.Fprintf(&b, "- `%s` — %s\n", t.Name, t.Description)
		}
	}
	return b.String()
}

func writeDiscovery(w http.ResponseWriter, r *http.Request, cfg *config.Config, mcpServer *server.MCPServer) {
	tools := listToolMeta(mcpServer)
	accept := r.Header.Get("Accept")
	switch {
	case strings.Contains(accept, "application/json"):
		writeJSONDiscovery(w, cfg, tools)
	case strings.Contains(accept, "text/markdown"):
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(GuideMarkdown(cfg, tools)))
	default:
		writeHTMLDiscovery(w, cfg, tools)
	}
}

func writeJSONDiscovery(w http.ResponseWriter, cfg *config.Config, tools []toolMeta) {
	base := cfg.PublicBaseURL()
	payload := map[string]any{
		"status":            "online",
		"name":              "Ibis Assistant",
		"version":           "1.1.0",
		"runtime_mode":      cfg.RuntimeMode,
		"listen_addr":       cfg.ListenAddr(),
		"protocol_versions": protocolVersions(),
		"endpoints": map[string]string{
			"streamable_http": base + "/mcp",
			"sse_legacy":      base + "/sse",
			"log_push_upload": base + "/api/v1/logs/upload",
			"guide_markdown":  base + "/",
			"guide_resource":  GuideURI,
			"server_discover": "server/discover",
		},
		"auth_headers": []string{
			"X-Git-Token", "X-Git-PAT",
			"X-Redmine-API-Key",
			"X-Jira-Email", "X-Jira-API-Token",
			"X-Azure-DevOps-PAT", "X-Azure-DevOps-Org", "X-Azure-DevOps-Project", "X-Azure-DevOps-Repo",
		},
		"documentation": map[string]any{
			"guide_resource": GuideURI,
			"cursor_mcp": map[string]any{
				"mcpServers": map[string]any{
					"ibis-assistant": map[string]any{
						"url": base + "/mcp",
					},
				},
			},
		},
		"guide_markdown": GuideMarkdown(cfg, tools),
		"tools":          tools,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func writeHTMLDiscovery(w http.ResponseWriter, cfg *config.Config, tools []toolMeta) {
	md := GuideMarkdown(cfg, tools)
	var toolsHTML strings.Builder
	for _, t := range tools {
		schemaBytes, _ := json.MarshalIndent(t.InputSchema, "", "  ")
		fmt.Fprintf(&toolsHTML, `<div class="tool-item"><div class="tool-name">%s</div><div class="tool-desc">%s</div><pre><code>%s</code></pre></div>`,
			html.EscapeString(t.Name), html.EscapeString(t.Description), html.EscapeString(string(schemaBytes)))
	}
	page := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<title>Ibis Assistant</title>
<style>
body{font-family:system-ui,sans-serif;background:#0f1419;color:#e7ecf3;margin:0;padding:2rem;}
main{max-width:52rem;margin:0 auto;}
pre{background:#1b222c;padding:1rem;overflow:auto;border-radius:8px;}
code{font-family:ui-monospace,monospace;}
.tool-item{margin:1rem 0;padding:1rem;background:#1b222c;border-radius:8px;}
.tool-name{font-weight:700;color:#7dd3fc;}
a{color:#7dd3fc;}
</style>
</head>
<body><main>
` + renderMarkdownAsPre(md) + `
<h2>Tool schemas</h2>
` + toolsHTML.String() + `
</main></body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

func renderMarkdownAsPre(md string) string {
	return "<pre>" + html.EscapeString(md) + "</pre>"
}

// RegisterGuideResource exposes the same markdown via MCP resources/read.
func RegisterGuideResource(s *server.MCPServer, cfg *config.Config) {
	if s == nil {
		return
	}
	s.AddResource(mcp.NewResource(GuideURI, "Ibis Assistant setup guide",
		mcp.WithMIMEType("text/markdown"),
	), func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		_ = ctx
		_ = request
		text := GuideMarkdown(cfg, listToolMeta(s))
		return []mcp.ResourceContents{mcp.TextResourceContents{
			URI:      GuideURI,
			MIMEType: "text/markdown",
			Text:     text,
		}}, nil
	})
}
