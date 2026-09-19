package config

import "strings"

// NormalizeReasoningConfig migrates legacy keys into clouds and fills routing defaults.
func NormalizeReasoningConfig(r *ReasoningConfig, defaultRPM int) {
	if r == nil {
		return
	}
	if strings.TrimSpace(r.Provider) == "" {
		r.Provider = "hybrid"
	}
	if strings.TrimSpace(r.Model) == "" {
		r.Model = "auto"
	}

	// Migrate legacy Keys / GeminiKeys-shaped data into clouds.gemini
	if len(r.Clouds.Gemini.Keys) == 0 && len(r.Keys) > 0 {
		r.Clouds.Gemini.Keys = append([]GeminiKeyConfig(nil), r.Keys...)
	}
	// Keep legacy Keys in sync for older code paths that still read them
	if len(r.Keys) == 0 && len(r.Clouds.Gemini.Keys) > 0 {
		r.Keys = append([]GeminiKeyConfig(nil), r.Clouds.Gemini.Keys...)
	}
	for i := range r.Clouds.Gemini.Keys {
		if r.Clouds.Gemini.Keys[i].RPM <= 0 && defaultRPM > 0 {
			r.Clouds.Gemini.Keys[i].RPM = defaultRPM
		}
	}

	if r.Routing.Mode == "" {
		r.Routing.Mode = "auto"
	}
	if r.Routing.LocalProvider == "" {
		r.Routing.LocalProvider = "ollama"
	}
	if r.Routing.CloudProvider == "" {
		r.Routing.CloudProvider = "auto"
	}
	if len(r.Routing.CloudFallbackOrder) == 0 {
		r.Routing.CloudFallbackOrder = []string{"gemini", "openai_compat", "claude"}
	}
	if r.Routing.LocalTimeoutMs <= 0 {
		r.Routing.LocalTimeoutMs = 120000
	}
	// use_cloud_when_no_gpu: default true only via NewDefaultConfig; preserve JSON false.

	if r.ModelOverrides == nil {
		r.ModelOverrides = map[string]string{}
	}
	defaults := map[string]string{
		"S":  "granite4.1:3b",
		"M":  "qwen2.5-coder:7b",
		"L":  "gemma4:12b",
		"XL": "muse-glimmer",
	}
	for k, v := range defaults {
		if strings.TrimSpace(r.ModelOverrides[k]) == "" {
			r.ModelOverrides[k] = v
		}
	}
	if strings.TrimSpace(r.Clouds.OpenAICompat.BaseURL) == "" {
		r.Clouds.OpenAICompat.BaseURL = "https://api.openai.com/v1"
	}
	if strings.TrimSpace(r.Clouds.OpenAICompat.Model) == "" {
		r.Clouds.OpenAICompat.Model = "gpt-4.1"
	}
	if strings.TrimSpace(r.Clouds.Claude.Model) == "" {
		r.Clouds.Claude.Model = "claude-sonnet-4"
	}
}

// GeminiKeysEffective returns gemini API keys from clouds or legacy Keys.
func (r ReasoningConfig) GeminiKeysEffective() []GeminiKeyConfig {
	if len(r.Clouds.Gemini.Keys) > 0 {
		return r.Clouds.Gemini.Keys
	}
	return r.Keys
}

// CloudHasCredentials reports whether a named cloud has usable credentials.
func (r ReasoningConfig) CloudHasCredentials(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "gemini":
		for _, k := range r.GeminiKeysEffective() {
			if strings.TrimSpace(k.Key) != "" {
				return true
			}
		}
	case "openai_compat", "openai":
		return strings.TrimSpace(r.Clouds.OpenAICompat.APIKey) != ""
	case "claude", "anthropic":
		return strings.TrimSpace(r.Clouds.Claude.APIKey) != ""
	}
	return false
}

// ConfiguredClouds returns cloud names that have credentials, in fallback order.
func (r ReasoningConfig) ConfiguredClouds() []string {
	order := r.Routing.CloudFallbackOrder
	if len(order) == 0 {
		order = []string{"gemini", "openai_compat", "claude"}
	}
	var out []string
	for _, name := range order {
		if r.CloudHasCredentials(name) {
			out = append(out, name)
		}
	}
	return out
}

// ResolveCloudPrimary returns the primary cloud id for routing.
func (r ReasoningConfig) ResolveCloudPrimary() string {
	forced := strings.ToLower(strings.TrimSpace(r.Routing.CloudProvider))
	if forced != "" && forced != "auto" {
		if r.CloudHasCredentials(forced) {
			return forced
		}
	}
	configured := r.ConfiguredClouds()
	if len(configured) == 0 {
		return ""
	}
	return configured[0]
}
