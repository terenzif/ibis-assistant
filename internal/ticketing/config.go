package ticketing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	DefaultProvider   ProviderName                    `json:"default_provider"`
	ProjectProviderMap map[string]ProviderName        `json:"project_provider_map"`
	Providers         ProviderConfigs                 `json:"providers"`
	Workflow          WorkflowConfig                  `json:"workflow"`
	ReferencePatterns map[string][]string             `json:"reference_patterns"`
	DefaultCreateType map[string]map[string]string    `json:"default_create_type"`
	PR                PRConfig                        `json:"pr"`
	HeaderHints       map[string][]string             `json:"header_hints"`
	ClientEnvHints    map[string]string               `json:"client_env_hints"`
}

type ProviderConfigs struct {
	Redmine      RedmineConfig      `json:"redmine"`
	Jira         JiraConfig         `json:"jira"`
	AzureDevOps  AzureDevOpsConfig  `json:"azure_devops"`
}

type RedmineConfig struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

type JiraConfig struct {
	BaseURL  string `json:"base_url"`
	Email    string `json:"email"`
	APIToken string `json:"api_token"`
}

type AzureDevOpsConfig struct {
	OrganizationURL string `json:"organization_url"`
	Project         string `json:"project"`
	Repository      string `json:"repository"`
	PAT             string `json:"pat"`
}

type WorkflowConfig struct {
	Actions map[string]WorkflowActionConfig `json:"actions"`
}

type WorkflowActionConfig struct {
	PerProvider map[ProviderName]map[string]string `json:"per_provider"`
}

type PRConfig struct {
	DefaultTargetBranch string            `json:"default_target_branch"`
	ProjectTargetBranch map[string]string `json:"project_target_branch"`
}

func defaultConfig() *Config {
	return &Config{
		DefaultProvider: ProviderRedmine,
		ProjectProviderMap: map[string]ProviderName{},
		ReferencePatterns: map[string][]string{},
		DefaultCreateType: map[string]map[string]string{},
		HeaderHints: map[string][]string{
			"redmine": {"X-Redmine-API-Key"},
			"jira": {"X-Jira-Email", "X-Jira-API-Token"},
			"azure_devops": {"X-Azure-DevOps-PAT"},
		},
		ClientEnvHints: map[string]string{},
	}
}

// NewDefaultConfig returns the baseline ticketing configuration used when no runtime file is available.
func NewDefaultConfig() *Config {
	cfg := defaultConfig()
	cfg.normalize()
	return cfg
}

func LoadConfigFromPath(path string) (*Config, error) {
	cfg := defaultConfig()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(cfg); err != nil {
		return nil, fmt.Errorf("decode ticketing config: %w", err)
	}
	cfg.normalize()
	return cfg, nil
}

func LoadConfig() (*Config, error) {
	path := filepath.Join("config", "ticketing_config.json")
	cfg, err := LoadConfigFromPath(path)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) normalize() {
	if c.DefaultProvider == "" {
		c.DefaultProvider = ProviderRedmine
	}
	if c.ProjectProviderMap == nil {
		c.ProjectProviderMap = map[string]ProviderName{}
	}
	if c.ReferencePatterns == nil {
		c.ReferencePatterns = map[string][]string{}
	}
	if c.DefaultCreateType == nil {
		c.DefaultCreateType = map[string]map[string]string{}
	}
	if c.Workflow.Actions == nil {
		c.Workflow.Actions = map[string]WorkflowActionConfig{}
	} else {
		normalized := make(map[string]WorkflowActionConfig, len(c.Workflow.Actions))
		for k, v := range c.Workflow.Actions {
			normalized[normalizeWorkflowAction(k)] = v
		}
		c.Workflow.Actions = normalized
	}
	for k, v := range c.ProjectProviderMap {
		c.ProjectProviderMap[strings.TrimSpace(k)] = ProviderName(strings.ToLower(strings.TrimSpace(string(v))))
	}
}
