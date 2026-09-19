package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all application configuration
type Config struct {
	Port                int                `json:"port"`
	BindAddress         string             `json:"bind_address,omitempty"`
	RuntimeMode         string             `json:"runtime_mode,omitempty"` // personal, server, plugin
	Mode                string             `json:"mode"`                   // sse, stdio
	DBUrl               string             `json:"db_url"`
	DBNamespace         string             `json:"db_namespace"`
	DBDatabase          string             `json:"db_database"`
	DBUser              string             `json:"db_user"`
	DBPassword          string             `json:"db_password"`
	AI                  AIConfig           `json:"ai"`
	GeminiKeys          []GeminiKeyConfig  `json:"gemini_keys,omitempty"` // Legacy fallback
	GeminiDefaultRPM    int                `json:"gemini_rpm,omitempty"`  // Legacy fallback
	RedmineURL          string             `json:"redmine_url"`
	RedmineKey          string             `json:"redmine_key"`
	RedmineConcurrency  int                `json:"redmine_concurrency"`
	CodeConcurrency     int                `json:"code_concurrency"`
	DBTimeout           int                `json:"db_timeout"`
	DBDataPath          string             `json:"db_data_path"`
	DBAutoUpdate        bool               `json:"db_auto_update"`
	DiscoveryRoot       string             `json:"discovery_root"`
	PollInterval        int                `json:"poll_interval"` // seconds
	AutoScan            bool               `json:"auto_scan"`
	GitRepos            []string           `json:"git_repos"` // Manual list override (absolute paths; match by folder basename)
	Projects            []ProjectConfig    `json:"projects,omitempty"`
	LogFile             string             `json:"log_file"`
	LogLevel            string             `json:"log_level"`      // DEBUG, INFO, WARN, ERROR
	MaxFileSize         int64              `json:"max_file_size"`  // bytes
	MaxDeltaSize        int                `json:"max_delta_size"` // bytes/characters for commit diff chunking
	IgnoredDirs         []string           `json:"ignored_dirs"`
	IgnoredFiles        []string           `json:"ignored_files"`
	SupportedExtensions []string           `json:"supported_extensions"`
	LogsRoot            string             `json:"logs_root"`
	GitTokens           map[string]string  `json:"git_tokens"`
	SMTP                SMTPConfig         `json:"smtp"`
	LogIngestion        LogIngestionConfig `json:"log_ingestion"`
	ConfigLoaded        bool               `json:"-"` // True if a config file was successfully loaded
	ConfigPath          string             `json:"-"` // Path to the file that was loaded
	Workspace           string             `json:"-"` // Opened folder / IBIS_WORKSPACE / -workspace
}

// ProjectConfig maps a project name to a live working tree or a server clone source.
type ProjectConfig struct {
	Name            string `json:"name"`
	WorkingRepoPath string `json:"working_repo_path,omitempty"`
	URL             string `json:"url,omitempty"`
	Branch          string `json:"branch,omitempty"`
}

type SMTPConfig struct {
	Enabled                    bool   `json:"enabled"`
	Host                       string `json:"host"`
	Port                       int    `json:"port"`
	User                       string `json:"user"`
	Password                   string `json:"password"`
	From                       string `json:"from"`
	To                         string `json:"to"`
	Encryption                 string `json:"encryption"` // ssl_tls, starttls, none
	AggregationWindow          string `json:"aggregation_window"`
	EmergencySeverityThreshold int    `json:"emergency_severity_threshold"`
}

type LogIngestionConfig struct {
	HTTP    HTTPIngestionConfig `json:"http"`
	Polling []PollingSource     `json:"polling"`
}

type HTTPIngestionConfig struct {
	Enabled bool   `json:"enabled"`
	APIKey  string `json:"api_key"`
}

type PollingSource struct {
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	Protocol     string `json:"protocol"` // ftp, sftp, smb
	Host         string `json:"host"`
	Port         int    `json:"port"`
	User         string `json:"user"`
	Password     string `json:"password"`
	RemoteDir    string `json:"remote_dir"`
	FilePattern  string `json:"file_pattern"`
	ArchiveDir   string `json:"archive_dir"`
	PollInterval string `json:"poll_interval"` // e.g. "15m"
	ProjectName  string `json:"project_name"`
	// KnownHostKey is the expected base64-encoded public key of the SFTP server
	// (e.g. "ssh-ed25519 AAAA..."). If empty, host key verification is skipped
	// (InsecureIgnoreHostKey). Set this in production to prevent MITM attacks.
	KnownHostKey string `json:"known_host_key"`
}

type GeminiKeyConfig struct {
	Key          string `json:"key"`
	RPM          int    `json:"rpm"`
	TPM          int    `json:"tpm"`
	RPD          int    `json:"rpd"`
	Owner        string `json:"owner"`
	AllowOverage bool   `json:"allow_overage"`
}

type AIConfig struct {
	Embedding EmbeddingConfig `json:"embedding"`
	Reasoning ReasoningConfig `json:"reasoning"`
}

type EmbeddingConfig struct {
	Provider   string `json:"provider"` // ollama, gemini
	Model      string `json:"model"`
	URL        string `json:"url"`
	AutoStart  bool   `json:"auto_start"`
	AutoUpdate bool   `json:"auto_update"`
}

type ReasoningConfig struct {
	Provider             string            `json:"provider"` // hybrid, ollama, gemini, openai_compat, claude, none
	Model                string            `json:"model"`    // auto or explicit Ollama/cloud model
	Keys                 []GeminiKeyConfig `json:"keys,omitempty"` // legacy; migrated into Clouds.Gemini
	Clouds               CloudsConfig      `json:"clouds,omitempty"`
	Routing              RoutingConfig     `json:"routing,omitempty"`
	ModelOverrides       map[string]string `json:"model_overrides,omitempty"` // S/M/L/XL
	ContextOnlyFallback  bool              `json:"context_only_fallback"`
	AlwaysSmallestLocal  bool              `json:"always_smallest_local,omitempty"`
}

// CloudsConfig holds multi-cloud credentials (plug-and-play via wizard/GUI).
type CloudsConfig struct {
	Gemini       GeminiCloudConfig       `json:"gemini,omitempty"`
	OpenAICompat OpenAICompatCloudConfig `json:"openai_compat,omitempty"`
	Claude       ClaudeCloudConfig       `json:"claude,omitempty"`
}

type GeminiCloudConfig struct {
	Keys []GeminiKeyConfig `json:"keys,omitempty"`
}

type OpenAICompatCloudConfig struct {
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
	Model   string `json:"model,omitempty"`
}

type ClaudeCloudConfig struct {
	APIKey string `json:"api_key,omitempty"`
	Model  string `json:"model,omitempty"`
}

// RoutingConfig controls hybrid local/cloud routing.
type RoutingConfig struct {
	Mode                string   `json:"mode,omitempty"` // auto
	LocalProvider       string   `json:"local_provider,omitempty"`
	CloudProvider       string   `json:"cloud_provider,omitempty"` // auto|gemini|openai_compat|claude
	CloudFallbackOrder  []string `json:"cloud_fallback_order,omitempty"`
	UseCloudWhenNoGPU   bool     `json:"use_cloud_when_no_gpu"`
	LocalTimeoutMs      int      `json:"local_timeout_ms,omitempty"`
}

// ListenAddr is host:port for the HTTP listener.
// Empty bind_address keeps today's ":port" (all interfaces) unless runtime_mode is set:
// personal/plugin → 127.0.0.1, server → 0.0.0.0.
func (c *Config) ListenAddr() string {
	host := strings.TrimSpace(c.BindAddress)
	if host == "" {
		switch strings.ToLower(strings.TrimSpace(c.RuntimeMode)) {
		case "personal", "plugin":
			host = "127.0.0.1"
		case "server":
			host = "0.0.0.0"
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(c.Port))
}

// PublicBaseURL is the URL advertised in discovery (loopback for wildcard binds).
func (c *Config) PublicBaseURL() string {
	host, _, err := net.SplitHostPort(c.ListenAddr())
	if err != nil || host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, strconv.Itoa(c.Port)))
}

func (c *Config) GetGitToken(originUrl string) string {
	if c.GitTokens == nil {
		return ""
	}

	// Try specific matches first
	for prefix, token := range c.GitTokens {
		if prefix != "*" && prefix != "default" && strings.Contains(originUrl, prefix) {
			return token
		}
	}

	// Fallback to default
	if token, ok := c.GitTokens["*"]; ok {
		return token
	}
	if token, ok := c.GitTokens["default"]; ok {
		return token
	}

	return ""
}

// Load returns the configuration loaded from Defaults + File + Env.
// It accepts optional config search paths.
func Load(paths ...string) *Config {
	// 1. Defaults
	cfg := &Config{
		Port:        3030,
		Mode:        "sse",
		DBUrl:       "ws://localhost:8000/rpc",
		DBNamespace: "ibisassistant",
		DBDatabase:  "analysis",
		DBUser:      "root",
		DBPassword:  "root",
		AI: AIConfig{
			Embedding: EmbeddingConfig{
				Provider:   "ollama",
				Model:      "nomic-embed-text",
				URL:        "http://127.0.0.1:11434",
				AutoStart:  true,
				AutoUpdate: true,
			},
			Reasoning: ReasoningConfig{
				Provider:            "hybrid",
				Model:               "auto",
				ContextOnlyFallback: true,
				Routing: RoutingConfig{
					Mode:               "auto",
					LocalProvider:      "ollama",
					CloudProvider:      "auto",
					CloudFallbackOrder: []string{"gemini", "openai_compat", "claude"},
					UseCloudWhenNoGPU:  true,
					LocalTimeoutMs:     120000,
				},
				ModelOverrides: map[string]string{
					"S":  "granite4.1:3b",
					"M":  "qwen2.5-coder:7b",
					"L":  "gemma4:12b",
					"XL": "muse-glimmer",
				},
				Clouds: CloudsConfig{
					OpenAICompat: OpenAICompatCloudConfig{
						BaseURL: "https://api.openai.com/v1",
						Model:   "gpt-4.1",
					},
					Claude: ClaudeCloudConfig{
						Model: "claude-sonnet-4",
					},
				},
			},
		},
		GeminiDefaultRPM:   100,
		RedmineConcurrency: 10,
		CodeConcurrency:    5,
		DBTimeout:          300,
		DBDataPath:         "db",
		DiscoveryRoot:      ".",
		PollInterval:       300,
		LogsRoot:           "./logs",
		AutoScan:           true,
		LogLevel:           "INFO",
		DBAutoUpdate:       true,
		MaxFileSize:        10 * 1024 * 1024, // 10MB
		MaxDeltaSize:       8000,
		IgnoredDirs: []string{
			".git", "node_modules", "bin", "obj", "vendor",
			".idea", ".vscode", "dist", "build", "coverage", "target",
		},
		IgnoredFiles: []string{
			"package-lock.json", "yarn.lock", "go.sum", "go.mod",
			"Gemfile.lock", "pnpm-lock.yaml",
		},
		SupportedExtensions: []string{
			".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".md", ".cs",
			".java", ".cpp", ".h", ".c", ".html", ".htm", ".css", ".scss", ".sql",
			".vue", ".aspx", ".ascx", ".cshtml", ".razor", ".jsp", ".erb", ".ejs",
			".svelte", ".astro", ".php", ".rb",
		},
		SMTP: SMTPConfig{
			Enabled:                    false,
			Encryption:                 "none",
			AggregationWindow:          "1h",
			EmergencySeverityThreshold: 9,
		},
		LogIngestion: LogIngestionConfig{
			HTTP: HTTPIngestionConfig{
				Enabled: false,
			},
			Polling: []PollingSource{},
		},
	}

	// 2. Candidate paths
	searchPaths := paths
	if len(searchPaths) == 0 {
		if envLooksLikePlugin() {
			// Do not load a personal install's config.json from CWD or next to the exe.
			searchPaths = pluginConfigSearchPaths()
		} else {
			searchPaths = []string{"config.json"}
			// If we are running as a service, the CWD might be wrong.
			// Try next to executable.
			if exe, err := os.Executable(); err == nil {
				searchPaths = append(searchPaths, filepath.Join(filepath.Dir(exe), "config.json"))
			}
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
	if v := os.Getenv("RUNTIME_MODE"); v != "" {
		cfg.RuntimeMode = v
	}
	if v := os.Getenv("BIND_ADDRESS"); v != "" {
		cfg.BindAddress = v
	}
	if v := os.Getenv("IBIS_WORKSPACE"); v != "" {
		cfg.Workspace = v
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
			// Update default rpm for key configs if present
			for i := range cfg.AI.Reasoning.Keys {
				cfg.AI.Reasoning.Keys[i].RPM = p
			}
		}
	}

	if v := os.Getenv("EMBEDDING_PROVIDER"); v != "" {
		cfg.AI.Embedding.Provider = v
	}
	if v := os.Getenv("EMBEDDING_MODEL"); v != "" {
		cfg.AI.Embedding.Model = v
	}
	if v := os.Getenv("EMBEDDING_URL"); v != "" {
		cfg.AI.Embedding.URL = v
	}
	if v := os.Getenv("REASONING_PROVIDER"); v != "" {
		cfg.AI.Reasoning.Provider = v
	}
	if v := os.Getenv("REASONING_MODEL"); v != "" {
		cfg.AI.Reasoning.Model = v
	}

	// Legacy fallback migration
	if len(cfg.GeminiKeys) > 0 && len(cfg.AI.Reasoning.Keys) == 0 {
		cfg.AI.Reasoning.Keys = cfg.GeminiKeys
	} else if len(cfg.AI.Reasoning.Keys) > 0 && len(cfg.GeminiKeys) == 0 {
		cfg.GeminiKeys = cfg.AI.Reasoning.Keys
	}

	NormalizeReasoningConfig(&cfg.AI.Reasoning, cfg.GeminiDefaultRPM)

	// Optional ENV key overrides only when file/clouds still empty (plug-and-play prefers wizard/GUI).
	if v := os.Getenv("GEMINI_API_KEY"); v != "" && !cfg.AI.Reasoning.CloudHasCredentials("gemini") {
		parts := strings.Split(v, ",")
		var keys []GeminiKeyConfig
		for _, p := range parts {
			clean := strings.TrimSpace(p)
			if clean != "" {
				keys = append(keys, GeminiKeyConfig{Key: clean, RPM: cfg.GeminiDefaultRPM})
			}
		}
		cfg.AI.Reasoning.Keys = keys
		cfg.GeminiKeys = keys
		cfg.AI.Reasoning.Clouds.Gemini.Keys = keys
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" && strings.TrimSpace(cfg.AI.Reasoning.Clouds.OpenAICompat.APIKey) == "" {
		cfg.AI.Reasoning.Clouds.OpenAICompat.APIKey = v
	}
	if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
		cfg.AI.Reasoning.Clouds.OpenAICompat.BaseURL = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" && strings.TrimSpace(cfg.AI.Reasoning.Clouds.Claude.APIKey) == "" {
		cfg.AI.Reasoning.Clouds.Claude.APIKey = v
	}

	// Legacy REDMINE_URL/REDMINE_API_KEY environment fallbacks intentionally removed.
	// Ticketing providers are configured via config/ticketing_config.json.
	if v := os.Getenv("REDMINE_CONCURRENCY"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.RedmineConcurrency = p
		}
	}
	if v := os.Getenv("CODE_CONCURRENCY"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.CodeConcurrency = p
		}
	}
	if v := os.Getenv("DB_TIMEOUT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.DBTimeout = p
		}
	}

	// Discovery
	if v := os.Getenv("DISCOVERY_ROOT"); v != "" {
		cfg.DiscoveryRoot = v
	}
	if v := os.Getenv("LOGS_ROOT"); v != "" {
		cfg.LogsRoot = v
	}
	if v := os.Getenv("AUTO_SCAN"); v == "true" {
		cfg.AutoScan = true
	}
	if v := os.Getenv("GIT_TOKEN"); v != "" {
		if cfg.GitTokens == nil {
			cfg.GitTokens = make(map[string]string)
		}
		cfg.GitTokens["*"] = v
	}
	if v := os.Getenv("LOG_FILE"); v != "" {
		cfg.LogFile = v
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.LogLevel = strings.ToUpper(v)
	}
	if v := os.Getenv("DB_AUTO_UPDATE"); v != "" {
		cfg.DBAutoUpdate = v == "true"
	}

	if cfg.IsPlugin() || envLooksLikePlugin() {
		cfg.RuntimeMode = "plugin"
		ApplyPluginIsolation(cfg)
	}

	// 5. Finalize paths (Service Mode Support)
	// If running as a service, CWD might be System32.
	// Resolve relative paths based on executable directory instead of CWD.
	// Plugin mode uses the isolated user-data directory instead.
	finalizeConfigPaths(cfg)

	return cfg
}

// NewDefaultConfig returns a Config populated with all defaults, without reading any file.
// Use this instead of Load("non_existent_path") when you only need the default values.
func NewDefaultConfig() *Config {
	return Load()
}

func resolvePath(baseDir, path string) string {
	// Custom absolute path check for cross-platform robustness (e.g. D:\path on non-Windows)
	isWinAbs := len(path) >= 3 && ((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z')) && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
	isUnixAbs := strings.HasPrefix(path, "/")
	if path == "" || filepath.IsAbs(path) || isWinAbs || isUnixAbs {
		return path
	}
	return filepath.Join(baseDir, path)
}
