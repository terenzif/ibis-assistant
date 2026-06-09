package repopr

import (
	"context"
	"testing"
)

type fakeProvider struct {
	name ProviderName
	lastCreate CreateParams
	lastComplete CompleteParams
}

func (f *fakeProvider) Name() ProviderName { return f.name }

func (f *fakeProvider) CreatePR(ctx context.Context, auth AuthContext, params CreateParams) (*PullRequest, error) {
	f.lastCreate = params
	return &PullRequest{Provider: f.name, ID: "10", Status: "active", Title: params.Title}, nil
}

func (f *fakeProvider) CompletePR(ctx context.Context, auth AuthContext, params CompleteParams) (*PullRequest, error) {
	f.lastComplete = params
	return &PullRequest{Provider: f.name, ID: params.PRID, Status: "completed"}, nil
}

func TestCreatePRAppliesTargetBranchAndAutodetect(t *testing.T) {
	svc := NewService(Config{
		DefaultProvider: ProviderAzureDevOps,
		DefaultTargetBranch: "develop",
	})
	provider := &fakeProvider{name: ProviderAzureDevOps}
	svc.RegisterProvider(provider)

	res, err := svc.CreatePR(context.Background(), CreateParams{SourceBranch: "feature/ABC-123-fix", Title: ""})
	if err != nil {
		t.Fatalf("CreatePR failed: %v", err)
	}
	if res.ID != "10" {
		t.Fatalf("unexpected result %+v", res)
	}
	if provider.lastCreate.TargetBranch != "develop" {
		t.Fatalf("expected default target branch develop, got %s", provider.lastCreate.TargetBranch)
	}
	if len(provider.lastCreate.TicketIDs) == 0 || provider.lastCreate.TicketIDs[0] != "ABC-123" {
		t.Fatalf("expected autodetected ticket ABC-123, got %+v", provider.lastCreate.TicketIDs)
	}
}

func TestProviderPrecedence(t *testing.T) {
	svc := NewService(Config{
		DefaultProvider: ProviderAzureDevOps,
		ProjectProviderMap: map[string]ProviderName{"proj": ProviderAzureDevOps},
	})
	provider := &fakeProvider{name: ProviderAzureDevOps}
	svc.RegisterProvider(provider)

	_, err := svc.CreatePR(context.Background(), CreateParams{Provider: "azure_devops", ProjectName: "proj", SourceBranch: "feature/x"})
	if err != nil {
		t.Fatalf("CreatePR failed: %v", err)
	}
	if provider.lastCreate.Provider != "azure_devops" {
		t.Fatalf("expected provider arg to propagate, got %s", provider.lastCreate.Provider)
	}
}
