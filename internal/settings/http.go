package settings

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/terenzif/ibis-assistant/internal/config"
	"github.com/terenzif/ibis-assistant/internal/settings/web"
)

// APIHandler serves GET/POST /api/v1/settings using the process config path.
func APIHandler(cfg *config.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleGet(w, cfg)
		case http.MethodPost:
			handlePost(w, r, cfg)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// UIHandler serves the embedded settings SPA under /settings/.
func UIHandler() http.Handler {
	return http.FileServer(http.FS(web.FS))
}

func handleGet(w http.ResponseWriter, cfg *config.Config) {
	if cfg == nil {
		cfg = config.NewDefaultConfig()
	}
	config.NormalizeReasoningConfig(&cfg.AI.Reasoning, cfg.GeminiDefaultRPM)
	probe := Probe(cfg.AI.Embedding.URL)
	rec := Recommend(cfg, probe)
	// Mask secrets in response copy
	view := *cfg
	view.AI.Reasoning.Keys = nil
	for i := range view.AI.Reasoning.Clouds.Gemini.Keys {
		view.AI.Reasoning.Clouds.Gemini.Keys[i].Key = MaskSecret(view.AI.Reasoning.Clouds.Gemini.Keys[i].Key)
	}
	view.AI.Reasoning.Clouds.OpenAICompat.APIKey = MaskSecret(view.AI.Reasoning.Clouds.OpenAICompat.APIKey)
	view.AI.Reasoning.Clouds.Claude.APIKey = MaskSecret(view.AI.Reasoning.Clouds.Claude.APIKey)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"probe":   probe,
		"summary": rec.Summary,
		"fields":  rec.Fields,
		"config":  view,
	})
}

type applyBody struct {
	AIMode              string `json:"ai_mode"`
	LocalModel          string `json:"local_model"`
	AlwaysSmallestLocal bool   `json:"always_smallest_local"`
	UseCloudWhenNoGPU   bool   `json:"use_cloud_when_no_gpu"`
	GeminiKey           string `json:"gemini_key"`
	OpenAIKey           string `json:"openai_key"`
	OpenAIBaseURL       string `json:"openai_base_url"`
	ClaudeKey           string `json:"claude_key"`
	DiscoveryRoot       string `json:"discovery_root"`
	LogsRoot            string `json:"logs_root"`
}

func handlePost(w http.ResponseWriter, r *http.Request, cfg *config.Config) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var body applyBody
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if cfg == nil {
		cfg = config.NewDefaultConfig()
	}
	probe := Probe(cfg.AI.Embedding.URL)
	use := body.UseCloudWhenNoGPU
	out, diff, err := Apply(cfg, UserChoices{
		AIMode:              body.AIMode,
		LocalModel:          body.LocalModel,
		AlwaysSmallestLocal: body.AlwaysSmallestLocal,
		UseCloudWhenNoGPU:   &use,
		GeminiKey:           body.GeminiKey,
		OpenAIKey:           body.OpenAIKey,
		OpenAIBaseURL:       body.OpenAIBaseURL,
		ClaudeKey:           body.ClaudeKey,
		DiscoveryRoot:       body.DiscoveryRoot,
		LogsRoot:            body.LogsRoot,
	}, probe)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	path := cfg.ConfigPath
	if path == "" {
		path = "config.json"
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, data, 0644); err != nil {
		// fallback cwd
		if err2 := os.WriteFile("config.json", data, 0644); err2 != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		path = "config.json"
	}
	*cfg = *out
	cfg.ConfigPath = path
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": path, "diff": diff})
}

// OpenURL returns the browser URL for settings on the given base (no trailing slash).
func OpenURL(base string) string {
	return strings.TrimRight(base, "/") + "/settings/"
}
