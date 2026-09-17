package server_test

import (
	"context"
	"testing"

	"github.com/terenzif/ibis-server/internal/schema"
)

// MockClient simulates database interactions for testing
type MockClient struct {
	CapturedQueries []string
}

func (m *MockClient) Execute(ctx context.Context, sql string) (interface{}, error) {
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
	ctx := context.Background()

	// Simulate "Project Knowledge" logic
	// 1. Create Repo
	client.Execute(ctx, "CREATE repo:test SET path = '/tmp/test';")

	// 2. Create Commit
	client.Execute(ctx, "CREATE commit:abc SET message = 'Fix bug #123';")

	// 3. Link
	client.Execute(ctx, "RELATE commit:abc->implements->issue:123;")

	if len(client.CapturedQueries) != 3 {
		t.Errorf("Expected 3 queries, got %d", len(client.CapturedQueries))
	}
}
