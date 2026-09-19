package wizard

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/terenzif/ibis-assistant/internal/config"
	"github.com/terenzif/ibis-assistant/internal/settings"
)

// Mode selects fast or full interactive setup.
type Mode string

const (
	ModeFast Mode = "fast"
	ModeFull Mode = "full"
)

// Run runs the fast wizard (default).
func Run() error {
	return RunMode(ModeFast)
}

// RunMode runs fast or full configuration wizard (English).
func RunMode(mode Mode) error {
	fmt.Println("\n=== Ibis Assistant — Configuration Wizard ===")
	fmt.Println("Leave a field empty to keep the recommended or current value.")

	cfg := config.NewDefaultConfig()
	if existing := tryLoadExisting(); existing != nil {
		cfg = existing
	}

	scanner := bufio.NewScanner(os.Stdin)
	var err error
	if mode == ModeFull {
		err = runFull(cfg, scanner)
	} else {
		err = runFast(cfg, scanner)
	}
	if err != nil {
		return fmt.Errorf("wizard interrupted: %w", err)
	}

	path := "config.json"
	if cfg.ConfigPath != "" {
		path = cfg.ConfigPath
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}
	fmt.Printf("\n[OK] Saved %s\n", path)

	fmt.Print("\nStart Ibis Assistant now? (Y/N) [Y]: ")
	scanner.Scan()
	start := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if start == "" || start == "y" || start == "yes" {
		fmt.Println("Starting server...")
		return nil
	}
	fmt.Println("Done. Run: ibis-assistant run")
	os.Exit(0)
	return nil
}

func tryLoadExisting() *config.Config {
	f, err := os.Open("config.json")
	if err != nil {
		return nil
	}
	defer f.Close()
	cfg := config.NewDefaultConfig()
	if err := json.NewDecoder(f).Decode(cfg); err != nil {
		return nil
	}
	cfg.ConfigLoaded = true
	cfg.ConfigPath, _ = filepath.Abs("config.json")
	config.NormalizeReasoningConfig(&cfg.AI.Reasoning, cfg.GeminiDefaultRPM)
	return cfg
}

func runFast(cfg *config.Config, scanner *bufio.Scanner) error {
	fmt.Println("\n--- Fast setup ---")
	promptRuntimeAndFolder(cfg, scanner)
	cfg.Port = promptInt("Server port", cfg.Port, scanner)

	ollamaURL := cfg.AI.Embedding.URL
	if ollamaURL == "" {
		ollamaURL = "http://127.0.0.1:11434"
	}
	probe := settings.Probe(ollamaURL)
	rec := settings.Recommend(cfg, probe)
	fmt.Printf("\nHardware probe:\n  GPU: %s (%s) VRAM≈%d MiB\n  Ollama reachable: %v library=%s\n  Tier: %s → %s\n  %s\n",
		probe.GPUName, probe.GPUVendor, probe.VRAMMiB, probe.OllamaReachable, probe.OllamaLibrary,
		probe.Tier, settings.ResolveLocalModel(cfg.AI.Reasoning, probe), rec.Summary)
	if probe.Diagnostic != "" {
		fmt.Printf("  Note: %s (install/fix ROCm if you expect AMD GPU acceleration)\n", probe.Diagnostic)
	}

	fmt.Print("\nAI mode:\n  1) Hybrid (recommended)\n  2) Local only (Ollama)\n  3) Cloud only (pick keys next)\n  4) Context only (retrieve; IDE agent answers)\nChoice (1-4) [1]: ")
	scanner.Scan()
	aiChoice := strings.TrimSpace(scanner.Text())
	if aiChoice == "" {
		aiChoice = "1"
	}
	choices := settings.UserChoices{}
	switch aiChoice {
	case "2":
		choices.AIMode = "ollama"
	case "3":
		choices.AIMode = "gemini"
	case "4":
		choices.AIMode = "none"
	default:
		choices.AIMode = "hybrid"
	}

	if choices.AIMode != "none" && choices.AIMode != "ollama" {
		fmt.Println("\nCloud API keys (paste into config — skip any you do not use):")
		fmt.Print("Gemini API key: ")
		scanner.Scan()
		choices.GeminiKey = strings.TrimSpace(scanner.Text())
		fmt.Print("OpenAI-compatible API key: ")
		scanner.Scan()
		choices.OpenAIKey = strings.TrimSpace(scanner.Text())
		if choices.OpenAIKey != "" {
			fmt.Print("OpenAI-compatible base URL [https://api.openai.com/v1]: ")
			scanner.Scan()
			choices.OpenAIBaseURL = strings.TrimSpace(scanner.Text())
		}
		fmt.Print("Claude (Anthropic) API key: ")
		scanner.Scan()
		choices.ClaudeKey = strings.TrimSpace(scanner.Text())
	}

	if choices.AIMode == "gemini" {
		switch {
		case choices.GeminiKey != "":
			choices.AIMode = "gemini"
		case choices.OpenAIKey != "":
			choices.AIMode = "openai_compat"
		case choices.ClaudeKey != "":
			choices.AIMode = "claude"
		default:
			choices.AIMode = "hybrid"
		}
	}

	fmt.Print("\nLocal model:\n  1) Automatic for this PC (recommended)\n  2) Always smallest (granite4.1:3b)\n  3) Pick tag\nChoice (1-3) [1]: ")
	scanner.Scan()
	lm := strings.TrimSpace(scanner.Text())
	if lm == "" {
		lm = "1"
	}
	switch lm {
	case "2":
		choices.AlwaysSmallestLocal = true
	case "3":
		fmt.Print("Ollama model tag: ")
		scanner.Scan()
		choices.LocalModel = strings.TrimSpace(scanner.Text())
	default:
		choices.LocalModel = "auto"
	}

	useCloud := promptBool("On CPU-only PCs, use the cloud for hard questions", true, scanner)
	choices.UseCloudWhenNoGPU = &useCloud

	cfg.DiscoveryRoot = promptString("Git discovery root", cfg.DiscoveryRoot, scanner)
	cfg.LogsRoot = promptString("Logs root", cfg.LogsRoot, scanner)
	choices.DiscoveryRoot = cfg.DiscoveryRoot
	choices.LogsRoot = cfg.LogsRoot
	choices.Port = cfg.Port
	choices.RuntimeMode = cfg.RuntimeMode
	if len(cfg.Projects) > 0 {
		choices.WorkingFolder = cfg.Projects[0].WorkingRepoPath
	}

	out, _, err := settings.Apply(cfg, choices, probe)
	if err != nil {
		return err
	}
	*cfg = *out
	_ = os.MkdirAll(cfg.DiscoveryRoot, 0755)
	_ = os.MkdirAll(cfg.LogsRoot, 0755)
	return nil
}

func runFull(cfg *config.Config, scanner *bufio.Scanner) error {
	if err := runFast(cfg, scanner); err != nil {
		return err
	}
	fmt.Println("\n--- Full setup (additional) ---")
	fmt.Println("[Database]")
	fmt.Print("  1) Embedded SurrealDB (recommended)\n  2) Remote SurrealDB\nChoice (1/2) [1]: ")
	scanner.Scan()
	dbc := strings.TrimSpace(scanner.Text())
	if dbc == "2" {
		cfg.DBUrl = promptString("DB URL", cfg.DBUrl, scanner)
		cfg.DBNamespace = promptString("Namespace", cfg.DBNamespace, scanner)
		cfg.DBDatabase = promptString("Database", cfg.DBDatabase, scanner)
		cfg.DBUser = promptString("User", cfg.DBUser, scanner)
		cfg.DBPassword = promptString("Password", cfg.DBPassword, scanner)
	} else {
		cfg.DBDataPath = promptString("DB data path", cfg.DBDataPath, scanner)
		cfg.DBAutoUpdate = promptBool("Auto-update SurrealDB", cfg.DBAutoUpdate, scanner)
	}

	fmt.Println("\n[Embeddings]")
	cfg.AI.Embedding.Provider = promptString("Embedding provider (ollama/gemini)", cfg.AI.Embedding.Provider, scanner)
	if cfg.AI.Embedding.Provider == "ollama" {
		cfg.AI.Embedding.URL = promptString("Ollama URL", cfg.AI.Embedding.URL, scanner)
		cfg.AI.Embedding.Model = promptString("Embedding model", cfg.AI.Embedding.Model, scanner)
		cfg.AI.Embedding.AutoStart = promptBool("Auto-start Ollama", cfg.AI.Embedding.AutoStart, scanner)
		cfg.AI.Embedding.AutoUpdate = promptBool("Auto-pull models", cfg.AI.Embedding.AutoUpdate, scanner)
	}

	fmt.Println("\n[Cloud advanced]")
	if cfg.AI.Reasoning.Clouds.OpenAICompat.APIKey != "" || promptBool("Configure OpenAI-compatible model/URL further", false, scanner) {
		cfg.AI.Reasoning.Clouds.OpenAICompat.BaseURL = promptString("OpenAI-compat base URL", cfg.AI.Reasoning.Clouds.OpenAICompat.BaseURL, scanner)
		cfg.AI.Reasoning.Clouds.OpenAICompat.Model = promptString("OpenAI-compat model", cfg.AI.Reasoning.Clouds.OpenAICompat.Model, scanner)
	}
	if cfg.AI.Reasoning.Clouds.Claude.APIKey != "" {
		cfg.AI.Reasoning.Clouds.Claude.Model = promptString("Claude model", cfg.AI.Reasoning.Clouds.Claude.Model, scanner)
	}
	cfg.AI.Reasoning.Routing.LocalTimeoutMs = promptInt("Local LLM timeout (ms)", cfg.AI.Reasoning.Routing.LocalTimeoutMs, scanner)

	fmt.Println("\n[SMTP]")
	cfg.SMTP.Enabled = promptBool("Enable SMTP alerts", cfg.SMTP.Enabled, scanner)
	if cfg.SMTP.Enabled {
		cfg.SMTP.Host = promptString("SMTP host", cfg.SMTP.Host, scanner)
		cfg.SMTP.Port = promptInt("SMTP port", cfg.SMTP.Port, scanner)
		cfg.SMTP.User = promptString("SMTP user", cfg.SMTP.User, scanner)
		cfg.SMTP.Password = promptString("SMTP password", cfg.SMTP.Password, scanner)
		cfg.SMTP.From = promptString("From", cfg.SMTP.From, scanner)
		cfg.SMTP.To = promptString("To", cfg.SMTP.To, scanner)
	}

	fmt.Println("\n[Ticketing]")
	if promptBool("Configure Redmine", false, scanner) {
		redmineURL := promptString("Redmine URL", "http://redmine.local", scanner)
		redmineKey := promptString("Redmine API key", "", scanner)
		cfg.RedmineURL = redmineURL
		cfg.RedmineKey = redmineKey
		ticketingConfig := map[string]interface{}{
			"default_provider":      "redmine",
			"project_provider_map": map[string]string{"*": "redmine"},
			"providers": map[string]interface{}{
				"redmine": map[string]string{"base_url": redmineURL, "api_key": redmineKey},
			},
			"pr": map[string]string{"default_target_branch": "master"},
		}
		_ = os.MkdirAll("config", 0755)
		tData, _ := json.MarshalIndent(ticketingConfig, "", "  ")
		_ = os.WriteFile(filepath.Join("config", "ticketing_config.json"), tData, 0644)
	}

	config.NormalizeReasoningConfig(&cfg.AI.Reasoning, cfg.GeminiDefaultRPM)
	return nil
}

// ShowAISummary prints a masked non-interactive AI config summary.
func ShowAISummary(cfg *config.Config) {
	if cfg == nil {
		cfg = config.NewDefaultConfig()
	}
	config.NormalizeReasoningConfig(&cfg.AI.Reasoning, cfg.GeminiDefaultRPM)
	probe := settings.Probe(cfg.AI.Embedding.URL)
	fmt.Printf("runtime_mode=%s port=%d\n", cfg.RuntimeMode, cfg.Port)
	fmt.Printf("reasoning.provider=%s model=%s resolved_local=%s\n",
		cfg.AI.Reasoning.Provider, cfg.AI.Reasoning.Model, settings.ResolveLocalModel(cfg.AI.Reasoning, probe))
	fmt.Printf("probe tier=%s vram_mib=%d cpu_only=%v\n", probe.Tier, probe.VRAMMiB, probe.CPUOnly)
	fmt.Printf("cloud_primary=%s use_cloud_when_no_gpu=%v\n",
		cfg.AI.Reasoning.ResolveCloudPrimary(), cfg.AI.Reasoning.Routing.UseCloudWhenNoGPU)
	fmt.Printf("gemini_keys=%d openai=%s claude=%s\n",
		len(cfg.AI.Reasoning.GeminiKeysEffective()),
		settings.MaskSecret(cfg.AI.Reasoning.Clouds.OpenAICompat.APIKey),
		settings.MaskSecret(cfg.AI.Reasoning.Clouds.Claude.APIKey))
	if cfg.Port > 0 {
		fmt.Printf("settings_ui=%s/settings\n", cfg.PublicBaseURL())
	}
}

// PrintRecommendationsJSON prints Recommend output as JSON.
func PrintRecommendationsJSON(cfg *config.Config) error {
	if cfg == nil {
		cfg = config.NewDefaultConfig()
	}
	probe := settings.Probe(cfg.AI.Embedding.URL)
	rec := settings.Recommend(cfg, probe)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(rec)
}

func promptRuntimeAndFolder(cfg *config.Config, scanner *bufio.Scanner) {
	mode := cfg.RuntimeMode
	if mode == "" {
		mode = "personal"
	}
	cfg.RuntimeMode = promptString("Runtime mode (personal/server/plugin)", mode, scanner)
	if !config.IsLiveRuntime(cfg.RuntimeMode) {
		return
	}
	cwd, _ := os.Getwd()
	folder := promptString("Local working folder", cwd, scanner)
	if strings.TrimSpace(folder) == "" {
		return
	}
	cfg.Projects = []config.ProjectConfig{{
		Name:            filepath.Base(folder),
		WorkingRepoPath: folder,
	}}
}

func promptString(prompt, defaultValue string, scanner *bufio.Scanner) string {
	fmt.Printf("%s [%s]: ", prompt, defaultValue)
	scanner.Scan()
	val := strings.TrimSpace(scanner.Text())
	if val == "" {
		return defaultValue
	}
	return val
}

func promptInt(prompt string, defaultValue int, scanner *bufio.Scanner) int {
	fmt.Printf("%s [%d]: ", prompt, defaultValue)
	scanner.Scan()
	val := strings.TrimSpace(scanner.Text())
	if val == "" {
		return defaultValue
	}
	num, err := strconv.Atoi(val)
	if err != nil {
		fmt.Printf("Invalid number, using default %d\n", defaultValue)
		return defaultValue
	}
	return num
}

func promptBool(prompt string, defaultValue bool, scanner *bufio.Scanner) bool {
	defStr := "Y"
	if !defaultValue {
		defStr = "N"
	}
	fmt.Printf("%s (Y/N) [%s]: ", prompt, defStr)
	scanner.Scan()
	val := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if val == "" {
		return defaultValue
	}
	return val == "y" || val == "yes" || val == "s" || val == "si"
}
