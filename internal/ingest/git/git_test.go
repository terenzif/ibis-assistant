package git

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/terenzif/ibis-server/internal/ticketing"
)

// MockDBClient for Git Ingestion
type MockDB struct {
	CapturedQueries []string
	MockResult      interface{}
	CapturedVars    []interface{}
}

func (m *MockDB) Execute(ctx context.Context, sql string) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	return m.MockResult, nil
}
func (m *MockDB) Close() {}
func (m *MockDB) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	m.CapturedVars = append(m.CapturedVars, vars)
	return m.MockResult, nil
}

// MockRedmineIngester (name preserved for existing tests) implements ticketing.Ingester.
type MockRedmine struct {
	IngestedIDs  []string
	IngestedRefs []ticketing.IssueReference
	Delay        time.Duration
}

func (m *MockRedmine) ResolveReference(projectKey string, ref ticketing.IssueReference) (ticketing.IssueReference, error) {
	if ref.Provider == "" {
		ref.Provider = ticketing.ProviderRedmine
	}
	if ref.ExternalKey == "" {
		ref.ExternalKey = ref.ExternalID
	}
	if ref.ExternalID == "" {
		ref.ExternalID = ref.ExternalKey
	}
	if ref.ProjectKey == "" {
		ref.ProjectKey = projectKey
	}
	return ref, nil
}

func (m *MockRedmine) IngestIssueReference(ctx context.Context, projectKey string, ref ticketing.IssueReference) error {
	if m.Delay > 0 {
		time.Sleep(m.Delay)
	}
	m.IngestedRefs = append(m.IngestedRefs, ref)
	m.IngestedIDs = append(m.IngestedIDs, ref.ExternalID)
	return nil
}

func helperExec(t *testing.T, dir, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Command %s %v failed: %v", name, args, err)
	}
}
