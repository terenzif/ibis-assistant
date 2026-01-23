package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

// Config holds all application configuration
type Config struct {
	Port          int      `json:"port"`
	Mode          string   `json:"mode"` // sse, stdio
	DBUrl         string   `json:"db_url"`
	DBNamespace   string   `json:"db_namespace"`
	DBDatabase    string   `json:"db_database"`
	DBUser        string   `json:"db_user"`
	DBPassword    string   `json:"db_password"`
	GeminiKeys    []string `json:"gemini_keys"`
	GeminiRPM     int      `json:"gemini_rpm"`
	RedmineURL    string   `json:"redmine_url"`
	RedmineKey    string   `json:"redmine_key"`
	DiscoveryRoot string   `json:"discovery_root"`
	AutoScan      bool     `json:"auto_scan"`
	GitRepos      []string `json:"git_repos"` // Manual list override
	LogFile       string   `json:"log_file"`
	LogLevel      string   `json:"log_level"` // DEBUG, INFO, WARN, ERROR
}

// Load returns the configuration loaded from Defaults + File + Env
func Load() *Config {
	// 1. Defaults
	cfg := &Config{
		Port:          3030,
		Mode:          "sse",
		DBUrl:         "ws://localhost:8000/rpc",
		DBNamespace:   "deckonline",
		DBDatabase:    "analysis",
		DBUser:        "root",
		DBPassword:    "root",
		GeminiRPM:     60,
		DiscoveryRoot: ".",
		AutoScan:      false,
		LogLevel:      "INFO",
	}

	// 2. Load from config.json if exists
	if f, err := os.Open("config.json"); err == nil {
		defer f.Close()
		decoder := json.NewDecoder(f)
		if err := decoder.Decode(cfg); err != nil {
			// Just log warning or ignore in this simplified loader
			// println("Warning: Failed to parse config.json")
		}
	}

	// 3. Env Overrides
	if v := os.Getenv("PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Port = p
		}
	}
	if v := os.Getenv("KNOWLEDGE_MODE"); v != "" {
		cfg.Mode = v
	}
	if v := os.Getenv("MODE"); v != "" { // Fallback
		cfg.Mode = v
	}

	// DB
	if v := os.Getenv("SURREAL_URL"); v != "" {
		cfg.DBUrl = v
	}
	if v := os.Getenv("SURREAL_NS"); v != "" {
		cfg.DBNamespace = v
	}
	if v := os.Getenv("SURREAL_DB"); v != "" {
		cfg.DBDatabase = v
	}
	if v := os.Getenv("SURREAL_USER"); v != "" {
		cfg.DBUser = v
	}
	if v := os.Getenv("SURREAL_PASS"); v != "" {
		cfg.DBPassword = v
	}

	// AI
	if v := os.Getenv("GEMINI_API_KEY"); v != "" {
		// Split by comma for pooling
		parts := strings.Split(v, ",")
		var keys []string
		for _, p := range parts {
			clean := strings.TrimSpace(p)
			if clean != "" {
				keys = append(keys, clean)
			}
		}
		cfg.GeminiKeys = keys // Replace file keys if ENV is set
	}
	if v := os.Getenv("KNOWLEDGE_RPM"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.GeminiRPM = p
		}
	}

	// Redmine
	if v := os.Getenv("REDMINE_URL"); v != "" {
		cfg.RedmineURL = v
	}
	if v := os.Getenv("REDMINE_API_KEY"); v != "" {
		cfg.RedmineKey = v
	}

	// Discovery
	if v := os.Getenv("DISCOVERY_ROOT"); v != "" {
		cfg.DiscoveryRoot = v
	}
	if v := os.Getenv("AUTO_SCAN"); v == "true" {
		cfg.AutoScan = true
	}
	if v := os.Getenv("LOG_FILE"); v != "" {
		cfg.LogFile = v
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.LogLevel = strings.ToUpper(v)
	}

	return cfg
}
