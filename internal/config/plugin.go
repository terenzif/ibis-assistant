package config

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultDBDataPath    = "db"
	defaultLogsRoot      = "./logs"
	defaultDBURL         = "ws://localhost:8000/rpc"
	defaultLoopbackDBURL = "ws://127.0.0.1:8000/rpc"
	defaultPluginDBURL   = "ws://127.0.0.1:18000/rpc"
	defaultPluginLogFile = "plugin.log"
)

// NormalizeRuntimeMode returns a lowercased runtime_mode (personal/server/plugin) or empty.
func NormalizeRuntimeMode(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

// IsLiveRuntime is true for personal and plugin (working tree in place, no destructive git).
func IsLiveRuntime(mode string) bool {
	switch NormalizeRuntimeMode(mode) {
	case "personal", "plugin":
		return true
	default:
		return false
	}
}

// IsPluginRuntime is true when runtime_mode is plugin.
func IsPluginRuntime(mode string) bool {
	return NormalizeRuntimeMode(mode) == "plugin"
}

func (c *Config) IsPlugin() bool {
	if c == nil {
		return false
	}
	return IsPluginRuntime(c.RuntimeMode)
}

func (c *Config) LiveWorkspace() bool {
	if c == nil {
		return false
	}
	return IsLiveRuntime(c.RuntimeMode)
}

// DataDir returns the isolated data directory for a runtime.
// IBIS_DATA_DIR overrides. Plugin default: %LOCALAPPDATA%\ibis-assistant\plugin
// (or the user config dir on non-Windows).
func DataDir(runtime string) string {
	if v := strings.TrimSpace(os.Getenv("IBIS_DATA_DIR")); v != "" {
		return v
	}
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		if d, err := os.UserConfigDir(); err == nil {
			base = d
		} else {
			base = os.TempDir()
		}
	}
	name := NormalizeRuntimeMode(runtime)
	if name == "" {
		name = "plugin"
	}
	return filepath.Join(base, "ibis-assistant", name)
}

func envLooksLikePlugin() bool {
	return IsPluginRuntime(os.Getenv("RUNTIME_MODE"))
}

func pluginConfigSearchPaths() []string {
	dir := DataDir("plugin")
	return []string{filepath.Join(dir, "config.json")}
}

func isDefaultRelative(path, def string) bool {
	p := filepath.ToSlash(strings.TrimSpace(path))
	d := filepath.ToSlash(strings.TrimSpace(def))
	if p == "" {
		return true
	}
	return strings.EqualFold(p, d) || strings.EqualFold(p, strings.TrimPrefix(d, "./"))
}

func isDefaultDBURL(url string) bool {
	u := strings.TrimSpace(url)
	return u == "" || strings.EqualFold(u, defaultDBURL) || strings.EqualFold(u, defaultLoopbackDBURL)
}

// ApplyPluginIsolation remaps default relative data paths into the plugin data dir
// so a Cursor child does not share SurrealDB or logs with a personal install.
func ApplyPluginIsolation(cfg *Config) {
	if cfg == nil || !cfg.IsPlugin() {
		return
	}
	dir := DataDir("plugin")
	_ = os.MkdirAll(dir, 0755)

	if isDefaultRelative(cfg.DBDataPath, defaultDBDataPath) || isPluginDefaultUnderExe(cfg.DBDataPath, defaultDBDataPath) {
		cfg.DBDataPath = filepath.Join(dir, "db")
	}
	if isDefaultRelative(cfg.LogsRoot, defaultLogsRoot) || isPluginDefaultUnderExe(cfg.LogsRoot, "logs") {
		cfg.LogsRoot = filepath.Join(dir, "logs")
	}
	if cfg.LogFile == "" || isDefaultRelative(cfg.LogFile, "./server.log") || isDefaultRelative(cfg.LogFile, "server.log") || isPluginDefaultUnderExe(cfg.LogFile, "server.log") {
		cfg.LogFile = filepath.Join(dir, defaultPluginLogFile)
	}
	if isDefaultDBURL(cfg.DBUrl) {
		cfg.DBUrl = defaultPluginDBURL
	}
}

func isPluginDefaultUnderExe(path, leaf string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	want := filepath.Clean(filepath.Join(filepath.Dir(exe), leaf))
	return strings.EqualFold(filepath.Clean(path), want)
}

func finalizeConfigPaths(cfg *Config) {
	plugin := cfg.IsPlugin() || envLooksLikePlugin()
	base := ""
	if plugin {
		base = DataDir("plugin")
		_ = os.MkdirAll(base, 0755)
	} else if exe, err := os.Executable(); err == nil {
		base = filepath.Dir(exe)
	}
	if base == "" {
		return
	}
	cfg.DBDataPath = resolvePath(base, cfg.DBDataPath)
	cfg.DiscoveryRoot = resolvePath(base, cfg.DiscoveryRoot)
	cfg.LogsRoot = resolvePath(base, cfg.LogsRoot)
	if cfg.LogFile != "" {
		cfg.LogFile = resolvePath(base, cfg.LogFile)
	}
	for i, p := range cfg.Projects {
		if p.WorkingRepoPath != "" {
			cfg.Projects[i].WorkingRepoPath = resolvePath(base, p.WorkingRepoPath)
		}
	}
}
