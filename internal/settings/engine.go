package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/terenzif/ibis-assistant/internal/config"
)

// Tier is the local model auto-selection band.
type Tier string

const (
	TierS  Tier = "S"
	TierM  Tier = "M"
	TierL  Tier = "L"
	TierXL Tier = "XL"
)

// HardwareReport is the result of a best-effort probe.
type HardwareReport struct {
	VRAMMiB           int64  `json:"vram_mib"`
	GPUName           string `json:"gpu_name,omitempty"`
	GPUVendor         string `json:"gpu_vendor,omitempty"` // amd, nvidia, apple, unknown
	OllamaLibrary     string `json:"ollama_library,omitempty"` // ROCm, CUDA, Vulkan, cpu
	OllamaReachable   bool   `json:"ollama_reachable"`
	CPUOnly           bool   `json:"cpu_only"`
	Tier              Tier   `json:"tier"`
	RecommendedModel  string `json:"recommended_model"`
	Diagnostic        string `json:"diagnostic,omitempty"`
}

// FieldRec is one recommended config field with a human reason.
type FieldRec struct {
	Path   string `json:"path"`
	Value  any    `json:"value"`
	Reason string `json:"reason"`
}

// Recommendations is the SettingsEngine output for UI/wizard.
type Recommendations struct {
	Probe   HardwareReport `json:"probe"`
	Fields  []FieldRec     `json:"fields"`
	Summary string         `json:"summary"`
}

// UserChoices are optional overrides from wizard/GUI.
type UserChoices struct {
	AIMode              string // hybrid|ollama|gemini|openai_compat|claude|none
	AlwaysSmallestLocal bool
	UseCloudWhenNoGPU   *bool
	GeminiKey           string
	OpenAIKey           string
	OpenAIBaseURL       string
	OpenAIModel         string
	ClaudeKey           string
	ClaudeModel         string
	LocalModel          string // auto|granite4.1:3b|explicit
	DiscoveryRoot       string
	LogsRoot            string
	RuntimeMode         string
	WorkingFolder       string
	Port                int
}

// DiffEntry describes one changed path.
type DiffEntry struct {
	Path string `json:"path"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Probe gathers hardware + Ollama status. ollamaURL may be empty.
func Probe(ollamaURL string) HardwareReport {
	if ollamaURL == "" {
		ollamaURL = "http://127.0.0.1:11434"
	}
	rep := HardwareReport{}

	if lib, vram, name, ok := probeOllama(ollamaURL); ok {
		rep.OllamaReachable = true
		rep.OllamaLibrary = lib
		rep.GPUName = name
		if vram > 0 {
			rep.VRAMMiB = vram
		}
		if strings.EqualFold(lib, "cpu") || lib == "" {
			rep.CPUOnly = true
		} else {
			rep.CPUOnly = false
			if strings.Contains(strings.ToLower(lib), "rocm") {
				rep.GPUVendor = "amd"
			} else if strings.Contains(strings.ToLower(lib), "cuda") {
				rep.GPUVendor = "nvidia"
			}
		}
	}

	if rep.VRAMMiB == 0 {
		if v, name, vendor := probeSystemGPU(); v > 0 {
			rep.VRAMMiB = v
			if rep.GPUName == "" {
				rep.GPUName = name
			}
			if rep.GPUVendor == "" {
				rep.GPUVendor = vendor
			}
			// Discrete GPU present but Ollama on CPU
			if rep.OllamaReachable && (strings.EqualFold(rep.OllamaLibrary, "cpu") || rep.OllamaLibrary == "") {
				rep.CPUOnly = true
				rep.Diagnostic = "gpu_present_but_ollama_cpu"
			} else if !rep.OllamaReachable {
				// Assume GPU usable until Ollama says otherwise
				rep.CPUOnly = false
			}
		} else if !rep.OllamaReachable {
			rep.CPUOnly = true
		}
	}

	if rep.CPUOnly || rep.VRAMMiB < 6*1024 {
		if rep.CPUOnly {
			rep.Tier = TierS
		} else if rep.VRAMMiB < 6*1024 {
			rep.Tier = TierS
		}
	}
	if !rep.CPUOnly {
		switch {
		case rep.VRAMMiB >= 22*1024:
			rep.Tier = TierXL
		case rep.VRAMMiB >= 10*1024:
			rep.Tier = TierL
		case rep.VRAMMiB >= 6*1024:
			rep.Tier = TierM
		default:
			rep.Tier = TierS
		}
	}
	if rep.Tier == "" {
		rep.Tier = TierS
	}
	rep.RecommendedModel = DefaultModelForTier(rep.Tier)
	return rep
}

// DefaultModelForTier returns the product default Ollama tag for a tier.
func DefaultModelForTier(t Tier) string {
	switch t {
	case TierM:
		return "qwen2.5-coder:7b"
	case TierL:
		return "gemma4:12b"
	case TierXL:
		return "muse-glimmer"
	default:
		return "granite4.1:3b"
	}
}

// ResolveLocalModel picks the Ollama chat model from config + probe.
func ResolveLocalModel(r config.ReasoningConfig, probe HardwareReport) string {
	if r.AlwaysSmallestLocal {
		if m := r.ModelOverrides["S"]; m != "" {
			return m
		}
		return "granite4.1:3b"
	}
	model := strings.TrimSpace(r.Model)
	if model != "" && !strings.EqualFold(model, "auto") {
		return model
	}
	tier := string(probe.Tier)
	if m := r.ModelOverrides[tier]; m != "" {
		return m
	}
	return DefaultModelForTier(probe.Tier)
}

// Recommend builds recommendations from existing config + probe.
func Recommend(existing *config.Config, probe HardwareReport) Recommendations {
	if existing == nil {
		existing = config.NewDefaultConfig()
	}
	config.NormalizeReasoningConfig(&existing.AI.Reasoning, existing.GeminiDefaultRPM)

	rec := Recommendations{Probe: probe}
	r := existing.AI.Reasoning

	provider := r.Provider
	if provider == "" || provider == "gemini" {
		// Suggest hybrid for new UX without forcing apply
		provider = "hybrid"
		rec.Fields = append(rec.Fields, FieldRec{
			Path: "ai.reasoning.provider", Value: "hybrid",
			Reason: "Hybrid is the product default: local for bulk, cloud for hard questions",
		})
	}

	localModel := ResolveLocalModel(r, probe)
	rec.Fields = append(rec.Fields, FieldRec{
		Path: "ai.reasoning.resolved_local_model", Value: localModel,
		Reason: fmt.Sprintf("Tier %s from probe (VRAM≈%d MiB, cpu_only=%v)", probe.Tier, probe.VRAMMiB, probe.CPUOnly),
	})

	primary := r.ResolveCloudPrimary()
	if primary == "" {
		rec.Fields = append(rec.Fields, FieldRec{
			Path: "ai.reasoning.routing.cloud_provider", Value: "auto",
			Reason: "No cloud keys configured yet; hybrid will use local only until you add keys in the wizard or Settings UI",
		})
	} else {
		rec.Fields = append(rec.Fields, FieldRec{
			Path: "ai.reasoning.routing.cloud_provider", Value: "auto",
			Reason: fmt.Sprintf("Resolved cloud primary: %s (from configured keys)", primary),
		})
	}

	useCloud := r.Routing.UseCloudWhenNoGPU
	if provider == "hybrid" && existing.AI.Reasoning.Routing.Mode == "" {
		useCloud = true
	}
	rec.Fields = append(rec.Fields, FieldRec{
		Path: "ai.reasoning.routing.use_cloud_when_no_gpu", Value: useCloud,
		Reason: "On CPU-only PCs, use the cloud for hard questions",
	})

	rec.Summary = fmt.Sprintf("Tier %s → %s; cloud primary=%s; provider=%s",
		probe.Tier, localModel, orEmpty(primary, "(none)"), provider)
	return rec
}

func orEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// Apply merges user choices into a copy of existing config.
func Apply(existing *config.Config, choices UserChoices, probe HardwareReport) (*config.Config, []DiffEntry, error) {
	if existing == nil {
		existing = config.NewDefaultConfig()
	}
	// Shallow copy via JSON
	raw, err := json.Marshal(existing)
	if err != nil {
		return nil, nil, err
	}
	cfg := config.NewDefaultConfig()
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, nil, err
	}
	before, _ := json.Marshal(cfg.AI.Reasoning)

	if choices.AIMode != "" {
		cfg.AI.Reasoning.Provider = choices.AIMode
	} else if cfg.AI.Reasoning.Provider == "" {
		cfg.AI.Reasoning.Provider = "hybrid"
	}
	cfg.AI.Reasoning.Model = "auto"
	if choices.LocalModel != "" && !strings.EqualFold(choices.LocalModel, "auto") {
		if choices.LocalModel == "granite4.1:3b" || choices.AlwaysSmallestLocal {
			cfg.AI.Reasoning.AlwaysSmallestLocal = true
			cfg.AI.Reasoning.Model = "auto"
		} else {
			cfg.AI.Reasoning.Model = choices.LocalModel
			cfg.AI.Reasoning.AlwaysSmallestLocal = false
		}
	}
	if choices.AlwaysSmallestLocal {
		cfg.AI.Reasoning.AlwaysSmallestLocal = true
	}
	if choices.UseCloudWhenNoGPU != nil {
		cfg.AI.Reasoning.Routing.UseCloudWhenNoGPU = *choices.UseCloudWhenNoGPU
	} else if cfg.AI.Reasoning.Provider == "hybrid" {
		cfg.AI.Reasoning.Routing.UseCloudWhenNoGPU = true
	}
	cfg.AI.Reasoning.ContextOnlyFallback = true
	cfg.AI.Reasoning.Routing.CloudProvider = "auto"

	if k := strings.TrimSpace(choices.GeminiKey); k != "" {
		cfg.AI.Reasoning.Clouds.Gemini.Keys = []config.GeminiKeyConfig{{Key: k, RPM: cfg.GeminiDefaultRPM, Owner: "default"}}
		cfg.AI.Reasoning.Keys = cfg.AI.Reasoning.Clouds.Gemini.Keys
		cfg.GeminiKeys = cfg.AI.Reasoning.Keys
	}
	if k := strings.TrimSpace(choices.OpenAIKey); k != "" {
		cfg.AI.Reasoning.Clouds.OpenAICompat.APIKey = k
	}
	if u := strings.TrimSpace(choices.OpenAIBaseURL); u != "" {
		cfg.AI.Reasoning.Clouds.OpenAICompat.BaseURL = u
	}
	if m := strings.TrimSpace(choices.OpenAIModel); m != "" {
		cfg.AI.Reasoning.Clouds.OpenAICompat.Model = m
	}
	if k := strings.TrimSpace(choices.ClaudeKey); k != "" {
		cfg.AI.Reasoning.Clouds.Claude.APIKey = k
	}
	if m := strings.TrimSpace(choices.ClaudeModel); m != "" {
		cfg.AI.Reasoning.Clouds.Claude.Model = m
	}
	if choices.DiscoveryRoot != "" {
		cfg.DiscoveryRoot = choices.DiscoveryRoot
	}
	if choices.LogsRoot != "" {
		cfg.LogsRoot = choices.LogsRoot
	}
	if choices.RuntimeMode != "" {
		cfg.RuntimeMode = choices.RuntimeMode
	}
	if choices.Port > 0 {
		cfg.Port = choices.Port
	}
	if choices.WorkingFolder != "" && config.IsLiveRuntime(cfg.RuntimeMode) {
		cfg.Projects = []config.ProjectConfig{{
			Name:            baseName(choices.WorkingFolder),
			WorkingRepoPath: choices.WorkingFolder,
		}}
	}

	config.NormalizeReasoningConfig(&cfg.AI.Reasoning, cfg.GeminiDefaultRPM)
	after, _ := json.Marshal(cfg.AI.Reasoning)
	diff := []DiffEntry{}
	if !bytes.Equal(before, after) {
		diff = append(diff, DiffEntry{Path: "ai.reasoning", From: "(previous)", To: "(updated)"})
	}
	_ = probe
	return cfg, diff, nil
}

func baseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 && i+1 < len(p) {
		return p[i+1:]
	}
	return p
}

// MaskSecret shows only the last 4 characters.
func MaskSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return "…" + s[len(s)-4:]
}

func probeOllama(base string) (library string, vramMiB int64, name string, ok bool) {
	client := &http.Client{Timeout: 2 * time.Second}
	// Ollama has no stable public GPU JSON; try /api/ps and /api/version as reachability.
	resp, err := client.Get(strings.TrimRight(base, "/") + "/api/version")
	if err != nil {
		return "", 0, "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", 0, "", false
	}
	// Best-effort: leave library unknown; system probe fills VRAM.
	return "unknown", 0, "", true
}

func probeSystemGPU() (vramMiB int64, name, vendor string) {
	if runtime.GOOS == "windows" {
		if v, n, vend := probeWindowsWMI(); v > 0 {
			return v, n, vend
		}
		if v, n := probeNvidiaSMI(); v > 0 {
			return v, n, "nvidia"
		}
	}
	return 0, "", ""
}

func probeNvidiaSMI() (int64, string) {
	out, err := exec.Command("nvidia-smi", "--query-gpu=memory.total,name", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0, ""
	}
	line := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	parts := strings.Split(line, ",")
	if len(parts) < 1 {
		return 0, ""
	}
	mib, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return 0, ""
	}
	name := ""
	if len(parts) > 1 {
		name = strings.TrimSpace(parts[1])
	}
	return mib, name
}

func probeWindowsWMI() (int64, string, string) {
	// AdapterRAM is bytes
	ps := `Get-CimInstance Win32_VideoController | Select-Object -First 1 Name,AdapterRAM | ConvertTo-Json -Compress`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", ps).Output()
	if err != nil {
		return 0, "", ""
	}
	var obj struct {
		Name       string `json:"Name"`
		AdapterRAM uint64 `json:"AdapterRAM"`
	}
	if err := json.Unmarshal(out, &obj); err != nil {
		return 0, "", ""
	}
	if obj.AdapterRAM == 0 {
		return 0, obj.Name, ""
	}
	mib := int64(obj.AdapterRAM / (1024 * 1024))
	vendor := "unknown"
	ln := strings.ToLower(obj.Name)
	if strings.Contains(ln, "amd") || strings.Contains(ln, "radeon") {
		vendor = "amd"
	} else if strings.Contains(ln, "nvidia") || strings.Contains(ln, "geforce") {
		vendor = "nvidia"
	} else if strings.Contains(ln, "intel") {
		vendor = "intel"
	}
	return mib, obj.Name, vendor
}
