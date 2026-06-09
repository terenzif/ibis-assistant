package ticketing

import "context"

// ProviderName identifies a ticketing backend implementation.
type ProviderName string

const (
	ProviderRedmine    ProviderName = "redmine"
	ProviderJira       ProviderName = "jira"
	ProviderAzureDevOps ProviderName = "azure_devops"
)

const (
	WorkflowResolve = "resolve"
	WorkflowClose   = "close"
	WorkflowReopen  = "reopen"
)

type Ticket struct {
	Provider       ProviderName            `json:"provider"`
	ID             string                  `json:"id,omitempty"`
	ExternalID     string                  `json:"external_id"`
	ExternalKey    string                  `json:"external_key"`
	ProjectKey     string                  `json:"project_key,omitempty"`
	Title          string                  `json:"title,omitempty"`
	Description    string                  `json:"description,omitempty"`
	Status         string                  `json:"status,omitempty"`
	Type           string                  `json:"type,omitempty"`
	Assignee       string                  `json:"assignee,omitempty"`
	Author         string                  `json:"author,omitempty"`
	URL            string                  `json:"url,omitempty"`
	CreatedOn      string                  `json:"created_on,omitempty"`
	UpdatedOn      string                  `json:"updated_on,omitempty"`
	ProviderFields map[string]interface{}  `json:"provider_fields,omitempty"`
}

type SearchParams struct {
	Provider    string `json:"provider,omitempty"`
	Query       string `json:"query,omitempty"`
	ProjectKey  string `json:"project_key,omitempty"`
	Status      string `json:"status,omitempty"`
	Type        string `json:"type,omitempty"`
	Assignee    string `json:"assignee,omitempty"`
	Author      string `json:"author,omitempty"`
	Priority    string `json:"priority,omitempty"`
	UpdatedFrom string `json:"updated_from,omitempty"`
	UpdatedTo   string `json:"updated_to,omitempty"`
	Sort        string `json:"sort,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Offset      int    `json:"offset,omitempty"`
}

type CreateParams struct {
	Provider           string                 `json:"provider,omitempty"`
	ProjectKey         string                 `json:"project_key,omitempty"`
	Title              string                 `json:"title"`
	Description        string                 `json:"description,omitempty"`
	Type               string                 `json:"type,omitempty"`
	Assignee           string                 `json:"assignee,omitempty"`
	Priority           string                 `json:"priority,omitempty"`
	ProviderFields     map[string]interface{} `json:"provider_fields,omitempty"`
	ProviderFieldsJSON string                 `json:"provider_fields_json,omitempty"`
}

type UpdateParams struct {
	Provider           string                 `json:"provider,omitempty"`
	Notes              string                 `json:"notes,omitempty"`
	Status             string                 `json:"status,omitempty"`
	Type               string                 `json:"type,omitempty"`
	Assignee           string                 `json:"assignee,omitempty"`
	Priority           string                 `json:"priority,omitempty"`
	WorkflowAction     string                 `json:"workflow_action,omitempty"`
	FixedVersion       string                 `json:"fixed_version,omitempty"`
	ProviderFields     map[string]interface{} `json:"provider_fields,omitempty"`
	ProviderFieldsJSON string                 `json:"provider_fields_json,omitempty"`
}

type SearchResult struct {
	Issues     []Ticket `json:"issues"`
	TotalCount int      `json:"total_count"`
	Offset     int      `json:"offset"`
	Limit      int      `json:"limit"`
}

type Status struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type Project struct {
	ID   string `json:"id"`
	Key  string `json:"key,omitempty"`
	Name string `json:"name"`
}

type WorkflowActionResult struct {
	Provider ProviderName `json:"provider"`
	Action   string       `json:"action"`
	Target   string       `json:"target"`
	IssueID  string       `json:"issue_id"`
}

type AuthContext struct {
	RedmineAPIKey  string
	JiraEmail      string
	JiraAPIToken   string
	AzureDevOpsPAT string
}

type IssueReference struct {
	Provider   ProviderName `json:"provider,omitempty"`
	ExternalID string       `json:"external_id,omitempty"`
	ExternalKey string      `json:"external_key,omitempty"`
	ProjectKey string       `json:"project_key,omitempty"`
	Confidence float64      `json:"confidence,omitempty"`
}

type TicketProvider interface {
	Name() ProviderName
	SearchIssues(ctx context.Context, auth AuthContext, params SearchParams) (*SearchResult, error)
	SearchMyIssues(ctx context.Context, auth AuthContext, params SearchParams) (*SearchResult, error)
	GetIssue(ctx context.Context, auth AuthContext, id string, projectKey string) (*Ticket, error)
	CreateIssue(ctx context.Context, auth AuthContext, params CreateParams) (*Ticket, error)
	UpdateIssue(ctx context.Context, auth AuthContext, id string, projectKey string, params UpdateParams) (*Ticket, error)
	AddComment(ctx context.Context, auth AuthContext, id string, projectKey string, comment string) error
	AssignIssue(ctx context.Context, auth AuthContext, id string, projectKey string, assignee string) error
	TransitionIssue(ctx context.Context, auth AuthContext, id string, projectKey string, transition string) error
	ListStatuses(ctx context.Context, auth AuthContext, projectKey string, issueType string) ([]Status, error)
	SearchUsers(ctx context.Context, auth AuthContext, query string, limit int) ([]User, error)
	ListProjects(ctx context.Context, auth AuthContext) ([]Project, error)
}

type Ingester interface {
	ResolveReference(projectKey string, ref IssueReference) (IssueReference, error)
	IngestIssueReference(ctx context.Context, projectKey string, ref IssueReference) error
}
