package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	if !strings.Contains(body["guide_markdown"].(string), "/mcp") {
		t.Fatal("guide missing /mcp")
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
