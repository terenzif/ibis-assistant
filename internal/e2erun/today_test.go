package e2erun

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/terenzif/ibis-assistant/internal/config"
	"github.com/terenzif/ibis-assistant/internal/httpserver"
	"github.com/terenzif/ibis-assistant/internal/ingest/dynamic"
	ibisruntime "github.com/terenzif/ibis-assistant/internal/runtime"
	"github.com/terenzif/ibis-assistant/internal/workspace"
)

func TestTodayE2E(t *testing.T) {
	t.Setenv("IBIS_E2E_RUN_ID", "e2e-today")
	live := initCommitRepo(t)

	t.Run("A_personal_live_tree", func(t *testing.T) {
		discovery := t.TempDir()
		cfg := &config.Config{
			RuntimeMode:   "personal",
			DiscoveryRoot: discovery,
			Projects:      []config.ProjectConfig{{Name: "demo", WorkingRepoPath: live}},
		}
		r, err := workspace.Resolve(cfg, "demo")
		if err != nil {
			t.Fatal(err)
		}
		if r.Owned {
			t.Fatal("personal must not be owned")
		}
		dirty := filepath.Join(live, "e2e-dirty.txt")
		if err := os.WriteFile(dirty, []byte("keep"), 0644); err != nil {
			t.Fatal(err)
		}
		path, _, _, err := dynamic.SyncWorkspace(context.Background(), cfg, nil, "demo", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if path != live {
			t.Fatalf("path=%s", path)
		}
		if _, err := os.Stat(filepath.Join(discovery, "dynamic", "demo")); err == nil {
			t.Fatal("cloned under dynamic/")
		}
		got, _ := os.ReadFile(dirty)
		if string(got) != "keep" {
			t.Fatalf("dirty file=%q", got)
		}
	})

	t.Run("A_server_clone", func(t *testing.T) {
		discovery := t.TempDir()
		cfg := &config.Config{RuntimeMode: "server", DiscoveryRoot: discovery}
		path, _, _, err := dynamic.SyncWorkspace(context.Background(), cfg, nil, "demo", live, "", "")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(discovery, "dynamic", "demo")
		if path != want {
			t.Fatalf("path=%s want %s", path, want)
		}
	})

	t.Run("B_plugin_isolation", func(t *testing.T) {
		cwd := t.TempDir()
		t.Chdir(cwd)
		personal := filepath.Join(cwd, "personal-db")
		payload, _ := json.Marshal(map[string]any{"db_data_path": personal, "runtime_mode": "personal"})
		if err := os.WriteFile(filepath.Join(cwd, "config.json"), payload, 0644); err != nil {
			t.Fatal(err)
		}
		data := t.TempDir()
		t.Setenv("RUNTIME_MODE", "plugin")
		t.Setenv("IBIS_DATA_DIR", data)
		cfg := config.Load()
		if strings.Contains(cfg.DBDataPath, "personal-db") {
			t.Fatalf("shared personal db: %s", cfg.DBDataPath)
		}
		if !strings.HasPrefix(cfg.DBDataPath, data) {
			t.Fatalf("DBDataPath=%s", cfg.DBDataPath)
		}
		if cfg.DBUrl != "ws://127.0.0.1:18000/rpc" {
			t.Fatalf("DBUrl=%s", cfg.DBUrl)
		}
	})

	t.Run("E_collision_port", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		_, portStr, err := net.SplitHostPort(ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		p, err := strconv.Atoi(portStr)
		if err != nil {
			t.Fatal(err)
		}
		if !ibisruntime.PortInUse("127.0.0.1", p) {
			t.Fatal("expected in use")
		}
		if ibisruntime.CollisionWarning(false, 0, true, p) == "" {
			t.Fatal("expected warning")
		}
	})

	t.Run("F_http_mcp_discovery", func(t *testing.T) {
		cfg := &config.Config{Port: 3030, RuntimeMode: "personal"}
		h := httpserver.Handler(cfg, server.NewMCPServer("t", "1"), nil)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		eps := body["endpoints"].(map[string]any)
		if eps["streamable_http"] != "http://127.0.0.1:3030/mcp" {
			t.Fatalf("eps=%v", eps)
		}
		req2 := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
		req2.Header.Set("Origin", "https://evil.example")
		rec2 := httptest.NewRecorder()
		h.ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusForbidden {
			t.Fatalf("origin status %d", rec2.Code)
		}
		req3 := httptest.NewRequest(http.MethodPost, "/api/v1/cli/call", strings.NewReader(`{}`))
		rec3 := httptest.NewRecorder()
		h.ServeHTTP(rec3, req3)
		if rec3.Code != http.StatusNotFound {
			t.Fatalf("cli/call %d", rec3.Code)
		}
	})

	t.Run("plugin_manifest", func(t *testing.T) {
		root := filepath.Join("..", "..", "cursor-plugin")
		raw, err := os.ReadFile(filepath.Join(root, "mcp.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"RUNTIME_MODE": "plugin"`) {
			t.Fatal("mcp.json missing RUNTIME_MODE")
		}
		if strings.Contains(string(raw), "${workspaceFolder}") {
			t.Fatal("mcp.json must not use ${workspaceFolder}; empty Cursor windows fail variable resolve")
		}
		if !strings.Contains(string(raw), `"-runtime-mode"`) {
			t.Fatal("mcp.json args must set -runtime-mode plugin")
		}
	})
}

func TestTodayE2EPluginBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("binary spawn")
	}
	t.Setenv("IBIS_E2E_RUN_ID", "e2e-today-plugin-bin")
	exe := filepath.Join(t.TempDir(), "ibis-assistant-e2e.exe")
	build := exec.Command("go", "build", "-o", exe, "./cmd/server")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:3030")
	if err == nil {
		defer ln.Close()
	}

	data := t.TempDir()
	cwd := t.TempDir()
	decoy := filepath.Join(cwd, "db")
	if err := os.MkdirAll(decoy, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decoy, "PERSONAL_MARKER"), []byte("personal"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-runtime-mode", "plugin", "-workspace", repo)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"RUNTIME_MODE=plugin",
		"IBIS_DATA_DIR="+data,
		"IBIS_WORKSPACE="+repo,
		"EMBEDDING_PROVIDER=none",
		"IBIS_E2E_RUN_ID=post-fix",
	)
	cmd.Stdin = strings.NewReader("")
	out, _ := cmd.CombinedOutput()
	if cmd.Process != nil {
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		time.Sleep(200 * time.Millisecond)
	}
	if !fileExists(filepath.Join(cwd, "db", "PERSONAL_MARKER")) {
		t.Fatal("decoy personal marker missing")
	}
	entries, _ := os.ReadDir(decoy)
	if len(entries) != 1 {
		t.Fatalf("plugin wrote into cwd db: %v", names(entries))
	}
	_ = out
}

func TestTodayE2EPluginBinaryNoWorkspace(t *testing.T) {
	if testing.Short() {
		t.Skip("binary spawn")
	}
	t.Setenv("IBIS_E2E_RUN_ID", "e2e-plugin-no-workspace")
	exe := filepath.Join(t.TempDir(), "ibis-assistant-e2e.exe")
	build := exec.Command("go", "build", "-o", exe, "./cmd/server")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	data := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-runtime-mode", "plugin")
	cmd.Dir = t.TempDir()
	cmd.Env = append(envWithout(os.Environ(), "IBIS_WORKSPACE"),
		"RUNTIME_MODE=plugin",
		"IBIS_DATA_DIR="+data,
		"EMBEDDING_PROVIDER=none",
		"IBIS_E2E_RUN_ID=post-fix",
	)
	cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}` + "\n")
	out, _ := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("plugin without workspace must complete MCP initialize, not hang")
	}
	if !strings.Contains(string(out), "jsonrpc") && !strings.Contains(string(out), "Ibis Assistant") {
		t.Fatalf("want MCP initialize on stdout, got %q", trimOut(out))
	}
}

func envWithout(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func trimOut(b []byte) string {
	s := string(b)
	if len(s) > 800 {
		s = s[:800]
	}
	return s
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func initCommitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("e2e"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
