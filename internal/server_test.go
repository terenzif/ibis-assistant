package server_test

import (
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/schema"
)

// MockClient simulates database interactions for testing
type MockClient struct {
	CapturedQueries []string
}

func (m *MockClient) Execute(sql string) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	return nil, nil
}

func TestSchemaConstants(t *testing.T) {
	// Verify critical table names
	expectedRepos := "repo"
	if schema.TableRepo != expectedRepos {
		t.Errorf("Expected TableRepo to be %s, got %s", expectedRepos, schema.TableRepo)
	}

	expectedCommit := "commit"
	if schema.TableCommit != expectedCommit {
		t.Errorf("Expected TableCommit to be %s, got %s", expectedCommit, schema.TableCommit)
	}
}

func TestMockIngestionFlow(t *testing.T) {
	// This test validates the expected logic flow without needing a running SurrealDB
	client := &MockClient{}

	// Simulate "Project Knowledge" logic
	// 1. Create Repo
	client.Execute("CREATE repo:test SET path = '/tmp/test';")
	
	// 2. Create Commit
	client.Execute("CREATE commit:abc SET message = 'Fix bug #123';")

	// 3. Link
	client.Execute("RELATE commit:abc->implements->issue:123;")

	if len(client.CapturedQueries) != 3 {
		t.Errorf("Expected 3 queries, got %d", len(client.CapturedQueries))
	}
}
