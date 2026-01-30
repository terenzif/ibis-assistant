package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all application configuration
type Config struct {
	Port          int               `json:"port"`
	Mode          string            `json:"mode"` // sse, stdio
	DBUrl         string            `json:"db_url"`
	DBNamespace   string            `json:"db_namespace"`
	DBDatabase    string            `json:"db_database"`
	DBUser        string            `json:"db_user"`
	DBPassword    string            `json:"db_password"`
	GeminiKeys    []GeminiKeyConfig `json:"gemini_keys"`
	GeminiDefaultRPM int            `json:"gemini_rpm"`
	RedmineURL    string            `json:"redmine_url"`
	RedmineKey    string            `json:"redmine_key"`
	DiscoveryRoot string            `json:"discovery_root"`
	AutoScan      bool     `json:"auto_scan"`
	GitRepos      []string `json:"git_repos"` // Manual list override
	LogFile       string   `json:"log_file"`
	LogLevel      string   `json:"log_level"` // DEBUG, INFO, WARN, ERROR
	ConfigLoaded  bool     `json:"-"`         // True if a config file was successfully loaded
	ConfigPath    string   `json:"-"`         // Path to the file that was loaded
}

type GeminiKeyConfig struct {
	Key   string `json:"key"`
	RPM   int    `json:"rpm"`
	TPM   int    `json:"tpm"`
	RPD   int    `json:"rpd"`
	Owner string `json:"owner"`
}

// Load returns the configuration loaded from Defaults + File + Env.
// It accepts optional config search paths.
func Load(paths ...string) *Config {
	// 1. Defaults
	cfg := &Config{
		Port:             3030,
		Mode:             "sse",
		DBUrl:            "ws://localhost:8000/rpc",
		DBNamespace:      "deckonline",
		DBDatabase:       "analysis",
		DBUser:           "root",
		DBPassword:       "root",
		GeminiDefaultRPM: 100,
		DiscoveryRoot:    ".",
		AutoScan:         true,
		LogLevel:         "INFO",
	}

	// 2. Candidate paths
	searchPaths := paths
	if len(searchPaths) == 0 {
		searchPaths = []string{"config.json"}
		// If we are running as a service, the CWD might be wrong.
		// Try next to executable.
		if exe, err := os.Executable(); err == nil {
			searchPaths = append(searchPaths, filepath.Join(filepath.Dir(exe), "config.json"))
		}
	}

	// 3. Load from first found config file
	for _, p := range searchPaths {
		if f, err := os.Open(p); err == nil {
			defer f.Close()
			decoder := json.NewDecoder(f)
			if err := decoder.Decode(cfg); err == nil {
				cfg.ConfigLoaded = true
				cfg.ConfigPath, _ = filepath.Abs(p)
				break 
			} else {
				// We found a file but it's invalid.
				// We print to stderr because logger isn't initialized yet.
				fmt.Fprintf(os.Stderr, "Error parsing config file %s: %v\n", p, err)
			}
		}
	}

	// 4. Env Overrides (Highest priority before CLI flags)
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
	if v := os.Getenv("KNOWLEDGE_RPM"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.GeminiDefaultRPM = p
		}
	}

	if v := os.Getenv("GEMINI_API_KEY"); v != "" {
		// Split by comma for pooling
		parts := strings.Split(v, ",")
		var keys []GeminiKeyConfig
		for _, p := range parts {
			clean := strings.TrimSpace(p)
			if clean != "" {
				keys = append(keys, GeminiKeyConfig{
					Key: clean,
					RPM: cfg.GeminiDefaultRPM,
				})
			}
		}
		cfg.GeminiKeys = keys // Replace file keys if ENV is set
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
