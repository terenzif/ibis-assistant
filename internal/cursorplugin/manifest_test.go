package cursorplugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCursorPluginManifest(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "cursor-plugin")
	raw, err := os.ReadFile(filepath.Join(root, ".cursor-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	name, _ := manifest["name"].(string)
	if name != "ibis-assistant" {
		t.Fatalf("name=%q", name)
	}
	mcpPath, _ := manifest["mcpServers"].(string)
	if mcpPath != "./mcp.json" {
		t.Fatalf("mcpServers=%q want ./mcp.json", mcpPath)
	}
	if strings.Contains(mcpPath, "..") || filepath.IsAbs(mcpPath) {
		t.Fatalf("mcpServers path must be relative and stay inside the plugin: %q", mcpPath)
	}

	mcpRaw, err := os.ReadFile(filepath.Join(root, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mcp map[string]any
	if err := json.Unmarshal(mcpRaw, &mcp); err != nil {
		t.Fatalf("mcp.json: %v", err)
	}
	servers, _ := mcp["mcpServers"].(map[string]any)
	ibis, _ := servers["ibis-assistant"].(map[string]any)
	if ibis["command"] != "ibis-assistant" {
		t.Fatalf("command=%v", ibis["command"])
	}
	args, _ := ibis["args"].([]any)
	if len(args) < 2 || args[0] != "-mode" || args[1] != "stdio" {
		t.Fatalf("args=%v want -mode stdio", args)
	}
	env, _ := ibis["env"].(map[string]any)
	if env["RUNTIME_MODE"] != "plugin" {
		t.Fatalf("env=%v", env)
	}
	if _, ok := env["IBIS_WORKSPACE"]; ok {
		t.Fatal("mcp.json must not interpolate ${workspaceFolder}; empty Cursor windows cannot resolve it")
	}
	if !strings.Contains(string(mcpRaw), `"-runtime-mode"`) {
		t.Fatal("args must set -runtime-mode plugin")
	}
}
