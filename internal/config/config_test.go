package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helper to clean up specific env vars
func cleanEnv() {
	vars := []string{
		"PORT", "KNOWLEDGE_MODE", "KNOWLEDGE_RPM",
		"GEMINI_API_KEY", "SURREAL_URL", "SURREAL_NS",
		"SURREAL_DB", "SURREAL_USER", "SURREAL_PASS",
		"REDMINE_URL", "REDMINE_API_KEY", "DISCOVERY_ROOT", "AUTO_SCAN",
		"DB_AUTO_UPDATE", "RUNTIME_MODE", "BIND_ADDRESS",
		"IBIS_DATA_DIR", "IBIS_WORKSPACE",
	}
	for _, v := range vars {
		os.Unsetenv(v)
	}
}

func TestLoadDefaults(t *testing.T) {
	cleanEnv()

	cfg := Load()

	if cfg.Port != 3030 {
		t.Errorf("Expected default Port 3030, got %d", cfg.Port)
	}
	if cfg.Mode != "sse" {
		t.Errorf("Expected default Mode sse, got %s", cfg.Mode)
	}
	if cfg.ListenAddr() != ":3030" {
		t.Errorf("Expected default ListenAddr :3030, got %s", cfg.ListenAddr())
	}
	if cfg.GeminiDefaultRPM != 100 {
		t.Errorf("Expected default RPM 100, got %d", cfg.GeminiDefaultRPM)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	cleanEnv()
	defer cleanEnv()

	os.Setenv("PORT", "4040")
	os.Setenv("KNOWLEDGE_MODE", "stdio")
	os.Setenv("KNOWLEDGE_RPM", "120")
	os.Setenv("GEMINI_API_KEY", "key1,   key2") // Test splitting
	os.Setenv("DB_AUTO_UPDATE", "false")

	cfg := Load()

	if cfg.Port != 4040 {
		t.Errorf("Expected Port 4040, got %d", cfg.Port)
	}
	if cfg.Mode != "stdio" {
		t.Errorf("Expected Mode stdio, got %s", cfg.Mode)
	}
	if cfg.GeminiDefaultRPM != 120 {
		t.Errorf("Expected RPM 120, got %d", cfg.GeminiDefaultRPM)
	}
	if cfg.DBAutoUpdate {
		t.Errorf("Expected DBAutoUpdate false from Env, got true")
	}

	if len(cfg.GeminiKeys) != 2 {
		t.Errorf("Expected 2 Gemini Keys, got %d", len(cfg.GeminiKeys))
	}
	if cfg.GeminiKeys[0].Key != "key1" || cfg.GeminiKeys[1].Key != "key2" {
		t.Errorf("Keys parsed incorrectly: %v", cfg.GeminiKeys)
	}
	if cfg.GeminiKeys[0].RPM != 120 {
		t.Errorf("Expected Key RPM 120, got %d", cfg.GeminiKeys[0].RPM)
	}
	// Env keys have no owner set
	if cfg.GeminiKeys[0].Owner != "" {
		t.Errorf("Expected empty owner for env keys, got %s", cfg.GeminiKeys[0].Owner)
	}
}

func TestLoadFileOverrides(t *testing.T) {
	cleanEnv()

	// Create temp config file
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	os.Chdir(tmpDir) // Change to temp dir so Load() finds config.json
	defer os.Chdir(originalWd)

	// Use map to avoid zero-value overwrite issues in test data generation
	fileConfig := map[string]interface{}{
		"port": 5050,
		"mode": "http",
	}
	data, _ := json.Marshal(fileConfig)
	os.WriteFile("config.json", data, 0644)

	cfg := Load()

	if cfg.Port != 5050 {
		t.Errorf("Expected Port 5050 from file, got %d", cfg.Port)
	}
	// Defaults should persist if not in file
	if cfg.GeminiDefaultRPM != 100 {
		t.Errorf("Expected default RPM 100, got %d", cfg.GeminiDefaultRPM)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	cleanEnv()
	defer cleanEnv()

	// Temp config file
	tmpDir := t.TempDir()
	originalWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(originalWd)

	fileConfig := map[string]interface{}{
		"port": 5050,
	}
	data, _ := json.Marshal(fileConfig)
	os.WriteFile("config.json", data, 0644)

	// Env var should win
	os.Setenv("PORT", "6060")

	cfg := Load()

	if cfg.Port != 6060 {
		t.Errorf("Expected Port 6060 from Env, got %d", cfg.Port)
	}
}

func TestResolvePath(t *testing.T) {
	baseDir := "C:\\app\\bin"

	tests := []struct {
		name     string
		baseDir  string
		path     string
		expected string
	}{
		{
			name:     "Absolute Path remains same",
			baseDir:  baseDir,
			path:     "D:\\data\\file.db",
			expected: "D:\\data\\file.db",
		},
		{
			name:     "Relative Path is resolved",
			baseDir:  baseDir,
			path:     "data/file.db",
			expected: filepath.Join(baseDir, "data/file.db"),
		},
		{
			name:     "Empty Path remains empty",
			baseDir:  baseDir,
			path:     "",
			expected: "",
		},
		{
			name:     "Unix absolute path",
			baseDir:  baseDir,
			path:     "/var/log/file.db",
			expected: "/var/log/file.db",
		},
		{
			name:     "Windows mixed slash absolute path",
			baseDir:  baseDir,
			path:     "E:/data\\file.db",
			expected: "E:/data\\file.db",
		},
		{
			name:     "Current directory dot",
			baseDir:  baseDir,
			path:     ".",
			expected: baseDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolvePath(tt.baseDir, tt.path)
			if got != tt.expected {
				t.Errorf("resolvePath() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestConfigLoadResolution(t *testing.T) {
	// We can't easily mock os.Executable() here without complex monkey patching,
	// but we can verify it doesn't crash and returns absolute paths.
	cfg := Load()

	if !filepath.IsAbs(cfg.DBDataPath) {
		t.Errorf("DBDataPath should be absolute, got %s", cfg.DBDataPath)
	}
	if !filepath.IsAbs(cfg.DiscoveryRoot) {
		t.Errorf("DiscoveryRoot should be absolute, got %s", cfg.DiscoveryRoot)
	}
	if !filepath.IsAbs(cfg.LogsRoot) {
		t.Errorf("LogsRoot should be absolute, got %s", cfg.LogsRoot)
	}
}

func TestListenAddrAndPublicURL(t *testing.T) {
	personal := &Config{Port: 3030, RuntimeMode: "personal"}
	if personal.ListenAddr() != "127.0.0.1:3030" {
		t.Fatalf("personal listen = %s", personal.ListenAddr())
	}
	server := &Config{Port: 3030, RuntimeMode: "server"}
	if server.ListenAddr() != "0.0.0.0:3030" {
		t.Fatalf("server listen = %s", server.ListenAddr())
	}
	custom := &Config{Port: 9, RuntimeMode: "personal", BindAddress: "10.0.0.5"}
	if custom.ListenAddr() != "10.0.0.5:9" {
		t.Fatalf("custom listen = %s", custom.ListenAddr())
	}
	if personal.PublicBaseURL() != "http://127.0.0.1:3030" {
		t.Fatalf("public = %s", personal.PublicBaseURL())
	}
}

func TestPluginIsolationUsesDataDir(t *testing.T) {
	cleanEnv()
	defer cleanEnv()
	dir := t.TempDir()
	t.Setenv("RUNTIME_MODE", "plugin")
	t.Setenv("IBIS_DATA_DIR", dir)
	cfg := Load()
	if !cfg.IsPlugin() {
		t.Fatal("expected plugin runtime")
	}
	if !strings.HasPrefix(cfg.DBDataPath, dir) {
		t.Fatalf("DBDataPath=%s want under %s", cfg.DBDataPath, dir)
	}
	if !strings.HasPrefix(cfg.LogsRoot, dir) {
		t.Fatalf("LogsRoot=%s want under %s", cfg.LogsRoot, dir)
	}
	if !strings.HasPrefix(cfg.LogFile, dir) {
		t.Fatalf("LogFile=%s want under %s", cfg.LogFile, dir)
	}
	if cfg.DBUrl != defaultPluginDBURL {
		t.Fatalf("DBUrl=%s want %s", cfg.DBUrl, defaultPluginDBURL)
	}
}

func TestPluginLoadIgnoresCWDConfig(t *testing.T) {
	cleanEnv()
	defer cleanEnv()
	cwd := t.TempDir()
	t.Chdir(cwd)
	personal := filepath.Join(cwd, "personal-db")
	payload, err := json.Marshal(map[string]any{
		"db_data_path": personal,
		"runtime_mode": "personal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "config.json"), payload, 0644); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	t.Setenv("RUNTIME_MODE", "plugin")
	t.Setenv("IBIS_DATA_DIR", data)
	cfg := Load()
	if strings.Contains(cfg.DBDataPath, "personal-db") {
		t.Fatalf("plugin loaded personal config: %s", cfg.DBDataPath)
	}
	if !strings.HasPrefix(cfg.DBDataPath, data) {
		t.Fatalf("DBDataPath=%s want under %s", cfg.DBDataPath, data)
	}
}
