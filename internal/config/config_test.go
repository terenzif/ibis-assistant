package config

import (
	"encoding/json"
	"os"
	"testing"
)

// Helper to clean up specific env vars
func cleanEnv() {
	vars := []string{
		"PORT", "KNOWLEDGE_MODE", "KNOWLEDGE_RPM", 
		"GEMINI_API_KEY", "SURREAL_URL", "SURREAL_NS", 
		"SURREAL_DB", "SURREAL_USER", "SURREAL_PASS",
		"REDMINE_URL", "REDMINE_API_KEY", "DISCOVERY_ROOT", "AUTO_SCAN",
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
	if cfg.GeminiRPM != 60 {
		t.Errorf("Expected default RPM 60, got %d", cfg.GeminiRPM)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	cleanEnv()
	defer cleanEnv()

	os.Setenv("PORT", "4040")
	os.Setenv("KNOWLEDGE_MODE", "stdio")
	os.Setenv("KNOWLEDGE_RPM", "120")
	os.Setenv("GEMINI_API_KEY", "key1,   key2") // Test splitting

	cfg := Load()

	if cfg.Port != 4040 {
		t.Errorf("Expected Port 4040, got %d", cfg.Port)
	}
	if cfg.Mode != "stdio" {
		t.Errorf("Expected Mode stdio, got %s", cfg.Mode)
	}
	if cfg.GeminiRPM != 120 {
		t.Errorf("Expected RPM 120, got %d", cfg.GeminiRPM)
	}
	
	if len(cfg.GeminiKeys) != 2 {
		t.Errorf("Expected 2 Gemini Keys, got %d", len(cfg.GeminiKeys))
	}
	if cfg.GeminiKeys[0] != "key1" || cfg.GeminiKeys[1] != "key2" {
		t.Errorf("Keys parsed incorrectly: %v", cfg.GeminiKeys)
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
	if cfg.GeminiRPM != 60 {
		t.Errorf("Expected default RPM 60, got %d", cfg.GeminiRPM)
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
