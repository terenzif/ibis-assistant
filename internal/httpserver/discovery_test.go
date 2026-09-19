package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/terenzif/ibis-assistant/internal/config"
)

func TestDiscoveryJSONAndMarkdown(t *testing.T) {
	cfg := &config.Config{Port: 3030, RuntimeMode: "personal"}
	h := Handler(cfg, server.NewMCPServer("t", "1"), nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("json status %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	eps, _ := body["endpoints"].(map[string]any)
	if eps["streamable_http"] != "http://127.0.0.1:3030/mcp" {
		t.Fatalf("endpoints=%v", eps)
	}
	if eps["settings_ui"] != "http://127.0.0.1:3030/settings/" {
		t.Fatalf("missing settings_ui: %v", eps)
	}
	guide := body["guide_markdown"].(string)
	if !strings.Contains(guide, "/mcp") {
		t.Fatal("guide missing /mcp")
	}
	if !strings.Contains(guide, "AI and Settings") || !strings.Contains(guide, "config fast") {
		t.Fatal("guide missing AI/Settings section")
	}
	vers, _ := body["protocol_versions"].([]any)
	foundLatest := false
	for _, v := range vers {
		if v == mcp.LATEST_PROTOCOL_VERSION {
			foundLatest = true
			break
		}
	}
	if !foundLatest {
		t.Fatalf("protocol_versions=%v", vers)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Accept", "text/markdown")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 200 || !strings.Contains(rec2.Body.String(), "Ibis Assistant") {
		t.Fatalf("markdown: %d %s", rec2.Code, rec2.Body.String())
	}
}

func TestOriginRejectedOnMCP(t *testing.T) {
	cfg := &config.Config{Port: 3030, RuntimeMode: "personal"}
	h := Handler(cfg, server.NewMCPServer("t", "1"), nil)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestCLICallRouteRemoved(t *testing.T) {
	cfg := &config.Config{Port: 3030, RuntimeMode: "personal"}
	h := Handler(cfg, server.NewMCPServer("t", "1"), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cli/call", strings.NewReader(`{"name":"ask_project"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cli/call should be gone, got %d", rec.Code)
	}
}

func TestSSEDeprecatedHeaders(t *testing.T) {
	h := withDeprecation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/sse", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Deprecation") != "true" {
		t.Fatalf("missing Deprecation, headers=%v", rec.Header())
	}
	if rec.Header().Get("Sunset") == "" {
		t.Fatal("missing Sunset")
	}
}

func TestMCPServerDiscover(t *testing.T) {
	cfg := &config.Config{Port: 3030, RuntimeMode: "personal"}
	s := server.NewMCPServer("t", "1", server.WithToolCapabilities(true), server.WithInstructions("guide"))
	ts := httptest.NewServer(Handler(cfg, s, nil))
	defer ts.Close()

	body := `{
	  "jsonrpc": "2.0",
	  "id": 1,
	  "method": "server/discover",
	  "params": {
	    "_meta": {
	      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
	      "io.modelcontextprotocol/clientInfo": {"name": "t", "version": "1"},
	      "io.modelcontextprotocol/clientCapabilities": {}
	    }
	  }
	}`
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(mcp.HeaderProtocolVersion, mcp.ProtocolVersion20260728)
	req.Header.Set(mcp.HeaderMethod, string(mcp.MethodServerDiscover))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var msg map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		t.Fatal(err)
	}
	result, _ := msg["result"].(map[string]any)
	if result == nil {
		t.Fatalf("no result: %v", msg)
	}
	versions, _ := result["supportedVersions"].([]any)
	found := false
	for _, v := range versions {
		if v == mcp.ProtocolVersion20260728 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("supportedVersions=%v", versions)
	}
}
