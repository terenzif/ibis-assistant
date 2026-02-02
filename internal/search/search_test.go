package search

import (
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/schema"
)

// MockDB implements db.Executor
type MockDB struct {
	ExecuteCalls []string
	SmartCalls   []string
	SmartVars    []interface{}
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
	m.SmartVars = append(m.SmartVars, vars)
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

func TestReinforcePath(t *testing.T) {
	mockDB := &MockDB{}
	svc := &Service{
		DB: mockDB,
		AI: &MockAI{},
	}

	// Test Case 1: Implements Edge (Commit -> Issue) with Positive Score
	err := svc.ReinforcePath("commit:1", "issue:1", 0.8)
	if err != nil {
		t.Fatalf("ReinforcePath failed: %v", err)
	}

	// Expect 2 SmartQuery calls:
	// 1. UPDATE edge_implements ... (weight + 0.1)
	// 2. UPDATE issue:1 ... (access_count + 1)
	if len(mockDB.SmartCalls) != 2 {
		t.Errorf("Expected 2 SmartCalls, got %d: %v", len(mockDB.SmartCalls), mockDB.SmartCalls)
	}

	if !strings.Contains(mockDB.SmartCalls[0], schema.EdgeImplements) {
		t.Errorf("Expected call 1 to update %s, got %s", schema.EdgeImplements, mockDB.SmartCalls[0])
	}
	if !strings.Contains(mockDB.SmartCalls[0], "+ 0.10") {
		t.Errorf("Expected call 1 to add 0.1, got %s", mockDB.SmartCalls[0])
	}

	if !strings.Contains(mockDB.SmartCalls[1], "access_count") {
		t.Errorf("Expected call 2 to update access_count, got %s", mockDB.SmartCalls[1])
	}

	// Reset
	mockDB.SmartCalls = nil

	// Test Case 2: Changed Edge (Commit -> File) with Negative Score
	err = svc.ReinforcePath("commit:1", "file:1", -0.5)
	if err != nil {
		t.Fatalf("ReinforcePath failed: %v", err)
	}

	// Expect 1 SmartQuery call:
	// 1. UPDATE edge_changed ... (weight - 0.1)
	// (No access_count update for negative score)
	if len(mockDB.SmartCalls) != 1 {
		t.Errorf("Expected 1 SmartCall, got %d: %v", len(mockDB.SmartCalls), mockDB.SmartCalls)
	}

	if !strings.Contains(mockDB.SmartCalls[0], schema.EdgeChanged) {
		t.Errorf("Expected call to update %s, got %s", schema.EdgeChanged, mockDB.SmartCalls[0])
	}
	if !strings.Contains(mockDB.SmartCalls[0], "- 0.10") { // might be formatted
		// The code uses %f, so it might be -0.100000.
		// " + -0.100000" because the query is `... + %f` and delta is -0.1.
		if !strings.Contains(mockDB.SmartCalls[0], "-0.1") {
			t.Errorf("Expected call to sub 0.1, got %s", mockDB.SmartCalls[0])
		}
	}
}
