package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mark3labs/mcp-go/server/servertest"
)

func TestCallMCPToolOverStreamableHTTP(t *testing.T) {
	mcpServer := server.NewMCPServer("t", "1", server.WithToolCapabilities(true))
	mcpServer.AddTool(mcp.NewTool("echo_tool",
		mcp.WithDescription("echo"),
		mcp.WithString("msg"),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := request.Params.Arguments.(map[string]any)
		msg, _ := args["msg"].(string)
		if request.Params.Meta == nil || request.Params.Meta.ProgressToken == nil {
			t.Error("missing progressToken")
		}
		return mcp.NewToolResultText(`{"ok":true,"msg":"` + msg + `"}`), nil
	})
	ts := servertest.NewTestStreamableHTTPServer(mcpServer)
	defer ts.Close()

	orig := mcpEndpoint
	mcpEndpoint = func() string { return ts.URL }
	defer func() { mcpEndpoint = orig }()

	if err := callMCPTool(context.Background(), ToolCallPayload{
		Name: "echo_tool",
		Arguments: map[string]interface{}{
			"msg": "hi",
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCallMCPToolUnknownTool(t *testing.T) {
	mcpServer := server.NewMCPServer("t", "1", server.WithToolCapabilities(true))
	ts := servertest.NewTestStreamableHTTPServer(mcpServer)
	defer ts.Close()

	orig := mcpEndpoint
	mcpEndpoint = func() string { return ts.URL }
	defer func() { mcpEndpoint = orig }()

	if err := callMCPTool(context.Background(), ToolCallPayload{Name: "nope", Arguments: map[string]interface{}{}}); err == nil {
		t.Fatal("expected error")
	}
}

func TestDefaultMCPEndpoint(t *testing.T) {
	u := defaultMCPEndpoint()
	if !strings.Contains(u, "/mcp") {
		t.Fatalf("endpoint %s", u)
	}
}
