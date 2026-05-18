package search

import (
	"context"
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/ai"
	
)

// MockDB implements db.Executor
type MockDB struct {
	ExecuteCalls []string
	SmartCalls   []string
	SmartVars    []interface{}
	ReturnData   map[string]interface{}
}

func (m *MockDB) Execute(ctx context.Context, sql string) (interface{}, error) {
	m.ExecuteCalls = append(m.ExecuteCalls, sql)
	// Check prefix
	for k, v := range m.ReturnData {
		if strings.Contains(sql, k) {
			return v, nil
		}
	}
	return nil, nil
}

func (m *MockDB) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	m.SmartCalls = append(m.SmartCalls, sql)
	m.SmartVars = append(m.SmartVars, vars)
	for k, v := range m.ReturnData {
		if strings.Contains(sql, k) {
			return v, nil
		}
	}
	return nil, nil
}

func (m *MockDB) Close() {}

// MockAI implements AIProvider
type MockAI struct {
	GenerateFunc func([]ai.Content) (ai.Candidate, error)
}

func (m *MockAI) EmbedText(ctx context.Context, text string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func (m *MockAI) GenerateContent(ctx context.Context, contents []ai.Content, config ai.GenerationConfig) (ai.Candidate, error) {
	if m.GenerateFunc != nil {
		return m.GenerateFunc(contents)
	}
	// Default response
	return ai.Candidate{
		Content: ai.Content{
			Parts: []ai.Part{{Text: "FINAL ANSWER: Mocked Default"}},
			Role:  "model",
		},
	}, nil
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
			"id": "commit:abc", "hash": "abc", "message": "init", "date": "2024-01-01",
			"author": []string{"me"},
			"issues": []map[string]interface{}{
				{"id": "1", "subject": "Fix", "status": "Closed", "weight": []float64{1.2}},
			},
		},
	}

	// change_edges for impact
	changeEdges := []map[string]interface{}{
		{"in": "commit:abc", "out": "file:main", "impact": 0.5},
	}

	graphResponse := []interface{}{
		map[string]interface{}{
			"history": graphHistory,
			"change_edges": changeEdges,
		},
	}

	mockDB := &MockDB{
		ReturnData: map[string]interface{}{
			"FROM [": chunks,
			"<-changed":       graphResponse,
		},
	}

	svc := &Service{
		DB: mockDB,
		AI: &MockAI{},
	}

	results, err := svc.AskProject(context.Background(), "test query")
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
	// Check Context Data
	if len(results[0].Context.ExpertAuthors) == 0 {
		t.Error("Expected expert authors, got empty")
	}
	if results[0].Context.ExpertAuthors[0] != "me" {
		t.Errorf("Expected author 'me', got %s", results[0].Context.ExpertAuthors[0])
	}

	if len(results[0].Context.RelatedIssues) == 0 {
		t.Error("Expected related issues, got empty")
	}
	// Check weight
	if results[0].Context.RelatedIssues[0].UsageWeight != 1.2 {
		t.Errorf("Expected issue weight 1.2, got %f", results[0].Context.RelatedIssues[0].UsageWeight)
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

	if len(extraRes.Context.ExpertAuthors) != 0 {
		t.Errorf("Expected 0 authors for 4th file, got %d", len(extraRes.Context.ExpertAuthors))
	}
}

