package git

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/db"
)

// MockDBClient for Git Ingestion
type MockDB struct {
	CapturedQueries []string
	MockResult      interface{}
	CapturedVars    []interface{}
}

func (m *MockDB) Execute(sql string) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	return m.MockResult, nil
}
func (m *MockDB) Close() {}
func (m *MockDB) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	m.CapturedVars = append(m.CapturedVars, vars)
	return m.MockResult, nil
}

// MockRedmineIngester for Git Ingestion
type MockRedmine struct {
	IngestedIDs []string
	Delay       time.Duration
}
func (m *MockRedmine) IngestIssue(ctx context.Context, dbClient db.Executor, issueIDStr string) error {
	if m.Delay > 0 {
		time.Sleep(m.Delay)
	}
	m.IngestedIDs = append(m.IngestedIDs, issueIDStr)
	return nil
}

func helperExec(t *testing.T, dir, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Command %s %v failed: %v", name, args, err)
	}
}
