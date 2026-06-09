package ticketing

import (
	"context"
	"testing"
)

type fakeProvider struct {
	name ProviderName
}

func (f *fakeProvider) Name() ProviderName { return f.name }

func (f *fakeProvider) SearchIssues(ctx context.Context, auth AuthContext, params SearchParams) (*SearchResult, error) {
	return &SearchResult{}, nil
}

func (f *fakeProvider) SearchMyIssues(ctx context.Context, auth AuthContext, params SearchParams) (*SearchResult, error) {
	return &SearchResult{}, nil
}

func (f *fakeProvider) GetIssue(ctx context.Context, auth AuthContext, id string, projectKey string) (*Ticket, error) {
	return &Ticket{Provider: f.name, ExternalID: id, ExternalKey: id}, nil
}

func (f *fakeProvider) CreateIssue(ctx context.Context, auth AuthContext, params CreateParams) (*Ticket, error) {
	return &Ticket{Provider: f.name, ExternalID: "1", ExternalKey: "1"}, nil
}

func (f *fakeProvider) UpdateIssue(ctx context.Context, auth AuthContext, id string, projectKey string, params UpdateParams) (*Ticket, error) {
	return &Ticket{Provider: f.name, ExternalID: id, ExternalKey: id}, nil
}

func (f *fakeProvider) AddComment(ctx context.Context, auth AuthContext, id string, projectKey string, comment string) error {
	return nil
}

func (f *fakeProvider) AssignIssue(ctx context.Context, auth AuthContext, id string, projectKey string, assignee string) error {
	return nil
}

func (f *fakeProvider) TransitionIssue(ctx context.Context, auth AuthContext, id string, projectKey string, transition string) error {
	return nil
}

func (f *fakeProvider) ListStatuses(ctx context.Context, auth AuthContext, projectKey string, issueType string) ([]Status, error) {
	return []Status{{ID: "2", Name: "Resolved"}, {ID: "3", Name: "Closed"}, {ID: "4", Name: "Reopened"}}, nil
}

func (f *fakeProvider) SearchUsers(ctx context.Context, auth AuthContext, query string, limit int) ([]User, error) {
	return []User{}, nil
}

func (f *fakeProvider) ListProjects(ctx context.Context, auth AuthContext) ([]Project, error) {
	return []Project{}, nil
}

func TestResolveProviderPrecedence(t *testing.T) {
	cfg := &Config{
		DefaultProvider: ProviderRedmine,
		ProjectProviderMap: map[string]ProviderName{
			"project-a": ProviderJira,
		},
	}

	svc := NewService(cfg)
	svc.RegisterProvider(&fakeProvider{name: ProviderRedmine})
	svc.RegisterProvider(&fakeProvider{name: ProviderJira})
	svc.RegisterProvider(&fakeProvider{name: ProviderAzureDevOps})

	provider, _, err := svc.resolveProvider("azure_devops", "project-a")
	if err != nil {
		t.Fatalf("resolve with arg failed: %v", err)
	}
	if provider != ProviderAzureDevOps {
		t.Fatalf("expected azure_devops from explicit arg, got %s", provider)
	}

	provider, _, err = svc.resolveProvider("", "project-a")
	if err != nil {
		t.Fatalf("resolve with project map failed: %v", err)
	}
	if provider != ProviderJira {
		t.Fatalf("expected jira from project map, got %s", provider)
	}

	provider, _, err = svc.resolveProvider("", "unknown-project")
	if err != nil {
		t.Fatalf("resolve with default failed: %v", err)
	}
	if provider != ProviderRedmine {
		t.Fatalf("expected redmine default, got %s", provider)
	}
}

func TestResolveWorkflowTargetHybrid(t *testing.T) {
	cfg := &Config{
		DefaultProvider: ProviderRedmine,
		Workflow: WorkflowConfig{
			Actions: map[string]WorkflowActionConfig{
				"resolved": {
					PerProvider: map[ProviderName]map[string]string{
						ProviderRedmine: {
							"*": "3",
						},
					},
				},
			},
		},
	}

	svc := NewService(cfg)
	svc.RegisterProvider(&fakeProvider{name: ProviderRedmine})
	svc.RegisterProvider(&fakeProvider{name: ProviderJira})
	svc.RegisterProvider(&fakeProvider{name: ProviderAzureDevOps})

	target, err := svc.resolveWorkflowTarget(context.Background(), ProviderRedmine, AuthContext{}, "resolved", "proj", "")
	if err != nil {
		t.Fatalf("resolve target failed: %v", err)
	}
	if target != "3" {
		t.Fatalf("expected explicit target 3, got %s", target)
	}

	target, err = svc.resolveWorkflowTarget(context.Background(), ProviderJira, AuthContext{}, "resolved", "proj", "")
	if err != nil {
		t.Fatalf("resolve fallback target failed: %v", err)
	}
	if target != "2" {
		t.Fatalf("expected heuristic target status id 2, got %s", target)
	}
}
