package repopr

import "context"

type ProviderName string

const (
	ProviderAzureDevOps ProviderName = "azure_devops"
)

type AuthContext struct {
	AzureDevOpsPAT string
}

type CreateParams struct {
	Provider      string   `json:"provider,omitempty"`
	ProjectName   string   `json:"project_name,omitempty"`
	OriginURL     string   `json:"origin_url,omitempty"`
	Repository    string   `json:"repository,omitempty"`
	SourceBranch  string   `json:"source_branch"`
	TargetBranch  string   `json:"target_branch,omitempty"`
	Title         string   `json:"title,omitempty"`
	Description   string   `json:"description,omitempty"`
	TicketIDs     []string `json:"ticket_ids,omitempty"`
	ReviewerIDs   []string `json:"reviewer_ids,omitempty"`
	AutoComplete  bool     `json:"auto_complete,omitempty"`
}

type CompleteParams struct {
	Provider    string `json:"provider,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	Repository  string `json:"repository,omitempty"`
	PRID        string `json:"pr_id"`
	DeleteSourceBranch bool `json:"delete_source_branch,omitempty"`
	Squash      bool   `json:"squash,omitempty"`
}

type PullRequest struct {
	Provider ProviderName `json:"provider"`
	ID       string       `json:"id"`
	URL      string       `json:"url,omitempty"`
	Title    string       `json:"title,omitempty"`
	Status   string       `json:"status,omitempty"`
}

type Provider interface {
	Name() ProviderName
	CreatePR(ctx context.Context, auth AuthContext, params CreateParams) (*PullRequest, error)
	CompletePR(ctx context.Context, auth AuthContext, params CompleteParams) (*PullRequest, error)
}

type Config struct {
	DefaultProvider    ProviderName            `json:"default_provider"`
	ProjectProviderMap map[string]ProviderName `json:"project_provider_map"`
	DefaultTargetBranch string                 `json:"default_target_branch"`
	ProjectTargetBranch map[string]string      `json:"project_target_branch"`
}
