package auth

type contextKey string

type TokenProvider interface {
	GetGitToken(originUrl string) string
}

const (
	// RedmineKeyContextKey is the key used to store/retrieve the Redmine API Key from context
	RedmineKeyContextKey contextKey = "redmine_api_key"
	// JiraEmailContextKey stores the Jira user email used for Basic authentication.
	JiraEmailContextKey contextKey = "jira_email"
	// JiraAPITokenContextKey stores the Jira API token used for Basic authentication.
	JiraAPITokenContextKey contextKey = "jira_api_token"
	// AzureDevOpsPATContextKey stores the Azure DevOps PAT used for API calls.
	AzureDevOpsPATContextKey contextKey = "azure_devops_pat"
	// AzureDevOpsOrgContextKey stores an optional Azure DevOps organization override.
	AzureDevOpsOrgContextKey contextKey = "azure_devops_org"
	// AzureDevOpsProjectContextKey stores an optional Azure DevOps project override.
	AzureDevOpsProjectContextKey contextKey = "azure_devops_project"
	// AzureDevOpsRepoContextKey stores an optional Azure DevOps repository override.
	AzureDevOpsRepoContextKey contextKey = "azure_devops_repo"
	// GitTokenContextKey stores an optional Git Token (PAT) override.
	GitTokenContextKey contextKey = "git_token"
	// TokenProviderKey is the key used to store/retrieve the token provider from context
	TokenProviderKey contextKey = "token_provider"
)

