package search

import (
	"strings"
	"testing"
)

// MockDB implements db.Executor
type MockDB struct {
	ExecuteCalls []string
	SmartCalls   []string
	ReturnData   map[string]interface{}
}

func (m *MockDB) Execute(sql string) (interface{}, error) {
	m.ExecuteCalls = append(m.ExecuteCalls, sql)
	// Check prefix
	for k, v := range m.ReturnData {
		if strings.Contains(sql, k) {
			return v, nil
		}
	}
	return nil, nil
}

func (m *MockDB) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	m.SmartCalls = append(m.SmartCalls, sql)
	for k, v := range m.ReturnData {
		if strings.Contains(sql, k) {
			return v, nil
		}
	}
	return nil, nil
}

func (m *MockDB) Close() {}

// MockAI implements Embedder
type MockAI struct{}

func (m *MockAI) EmbedText(text string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func TestAskProject(t *testing.T) {
	// 1. Mock Data for Vector Search
	chunks := []map[string]interface{}{
		{"id": "chunk:1", "path": "/src/main.go", "content": "func main()", "score": 0.9},
		{"id": "chunk:2", "path": "/src/main.go", "content": "var x = 1", "score": 0.8},
		{"id": "chunk:3", "path": "/src/utils.go", "content": "func help()", "score": 0.7},
		{"id": "chunk:4", "path": "/src/auth.go", "content": "func login()", "score": 0.6},
		{"id": "chunk:5", "path": "/src/extra.go", "content": "func extra()", "score": 0.5},
	}

	// 2. Mock Data for Graph Context
	graphHistory := []map[string]interface{}{
		{
			"hash": "abc", "message": "init", "date": "2024-01-01",
			"author": []string{"me"},
			"issues": []map[string]interface{}{
				{"id": "1", "subject": "Fix", "status": "Closed"},
			},
		},
	}

	graphResponse := []interface{}{
		map[string]interface{}{
			"history": graphHistory,
		},
	}

	mockDB := &MockDB{
		ReturnData: map[string]interface{}{
			"FROM file_chunk": chunks,
			"<-changed":       graphResponse,
		},
	}

	svc := &Service{
		DB: mockDB,
		AI: &MockAI{},
	}

	results, err := svc.AskProject("test query")
	if err != nil {
		t.Fatalf("AskProject failed: %v", err)
	}

	// Assertions
	if len(results) != 5 {
		t.Errorf("Expected 5 results, got %d", len(results))
	}

	// Check top result context
	if results[0].Path != "/src/main.go" {
		t.Errorf("Expected top result path /src/main.go, got %s", results[0].Path)
	}
	if results[0].Context == nil || len(results[0].Context.Commits) == 0 {
		t.Error("Expected context for top result, got empty/nil")
	}
	if results[0].Context.Commits[0].Hash != "abc" {
		t.Errorf("Expected commit hash 'abc', got %s", results[0].Context.Commits[0].Hash)
	}

	// Check issue parsing
	if results[0].Context.Issues[0].Status != "Closed" {
		t.Errorf("Expected issue status 'Closed', got %s", results[0].Context.Issues[0].Status)
	}

	// Check limit (Top 3 files)
	// /src/extra.go is 4th unique file. Should NOT have context.
	var extraRes Result
	found := false
	for _, r := range results {
		if r.Path == "/src/extra.go" {
			extraRes = r
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Could not find /src/extra.go in results")
	}

	if len(extraRes.Context.Commits) != 0 {
		t.Errorf("Expected 0 commits for 4th file, got %d", len(extraRes.Context.Commits))
	}
}
