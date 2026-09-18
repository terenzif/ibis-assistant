package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// mcpEndpoint returns the Streamable HTTP URL of a running Ibis Assistant server.
var mcpEndpoint = defaultMCPEndpoint

func defaultMCPEndpoint() string {
	port := 3030
	if f, err := os.Open("config.json"); err == nil {
		defer f.Close()
		var cfg struct {
			Port int `json:"port"`
		}
		if err := json.NewDecoder(f).Decode(&cfg); err == nil && cfg.Port > 0 {
			port = cfg.Port
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
}

// callServerTool forwards a CLI subcommand as MCP tools/call over Streamable HTTP.
var callServerTool = func(payload ToolCallPayload) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := callMCPTool(ctx, payload); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func callMCPTool(ctx context.Context, payload ToolCallPayload) error {
	c, err := mcpclient.NewStreamableHttpClient(mcpEndpoint())
	if err != nil {
		return fmt.Errorf("failed to create MCP client: %w", err)
	}
	defer c.Close()

	c.OnNotification(func(n mcp.JSONRPCNotification) {
		if n.Method != "notifications/progress" {
			return
		}
		msg, _ := n.Params.AdditionalFields["message"].(string)
		if msg == "" {
			return
		}
		progress, _ := n.Params.AdditionalFields["progress"]
		total, _ := n.Params.AdditionalFields["total"]
		if total != nil {
			fmt.Fprintf(os.Stderr, "[progress %v/%v] %s\n", progress, total, msg)
			return
		}
		fmt.Fprintf(os.Stderr, "[progress] %s\n", msg)
	})

	if err := c.Start(ctx); err != nil {
		return fmt.Errorf("failed to start MCP client: %w\nMake sure the server is running (ibis-assistant start or ibis-assistant run).", err)
	}

	_, err = c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "ibis-assistant-cli",
				Version: "1.1.0",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("MCP initialize failed: %w\nMake sure the server is running (ibis-assistant start or ibis-assistant run).", err)
	}

	result, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      payload.Name,
			Arguments: payload.Arguments,
			Meta:      &mcp.Meta{ProgressToken: payload.Name},
		},
	})
	if err != nil {
		return fmt.Errorf("MCP tools/call %s failed: %w", payload.Name, err)
	}
	if result.IsError {
		fmt.Fprintln(os.Stderr, "Tool returned an error:")
		printToolContent(os.Stderr, result)
		return fmt.Errorf("tool %s failed", payload.Name)
	}
	printToolContent(os.Stdout, result)
	return nil
}

func printToolContent(w io.Writer, result *mcp.CallToolResult) {
	if result == nil {
		return
	}
	for _, item := range result.Content {
		if tc, ok := mcp.AsTextContent(item); ok {
			printMaybeJSON(w, tc.Text)
			continue
		}
		b, err := json.MarshalIndent(item, "", "  ")
		if err != nil {
			fmt.Fprintln(w, item)
			continue
		}
		fmt.Fprintln(w, string(b))
	}
}

func printMaybeJSON(w io.Writer, text string) {
	trimmed := strings.TrimSpace(text)
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		var js any
		if err := json.Unmarshal([]byte(trimmed), &js); err == nil {
			formatted, err2 := json.MarshalIndent(js, "", "  ")
			if err2 == nil {
				fmt.Fprintln(w, string(formatted))
				return
			}
		}
	}
	fmt.Fprintln(w, text)
}
