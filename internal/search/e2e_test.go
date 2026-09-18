package search

import (
	"context"
	"strings"
	"testing"

	
)

// CapturingDB is a MockDB that captures queries and returns specific mocked data based on query content.
type CapturingDB struct {
	CapturedQueries []string
	CapturedVars    []interface{}
	ReturnData      map[string]interface{}
}

func (m *CapturingDB) Execute(ctx context.Context, sql string) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	m.CapturedVars = append(m.CapturedVars, nil) // Keep aligned
	// Check return data
	for k, v := range m.ReturnData {
		if strings.Contains(sql, k) {
			return v, nil
		}
	}
	return nil, nil
}

func (m *CapturingDB) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	m.CapturedVars = append(m.CapturedVars, vars)
	for k, v := range m.ReturnData {
		if strings.Contains(sql, k) {
			return v, nil
		}
	}
	return nil, nil
}

func (m *CapturingDB) Close() {}

func TestEndToEnd_SearchReinforcement(t *testing.T) {
	// 1. Setup Mock Data
	fileID := "file:path_to_main_go"
	commitID := "commit:hash123"
	issueID := "issue:42"

	// Mock Vector Search Response
	chunks := []interface{}{
		map[string]interface{}{
			"id":      "chunk:1",
			"path":    "/path/to/main.go",
			"content": "package main",
			"score":   0.9,
		},
	}

	// Mock Graph Response (GetFileContext)
	// Query contains "<-changed as change_edges"
	graphResponse := []interface{}{
		map[string]interface{}{
			"change_edges": []interface{}{
				map[string]interface{}{
					"in":     commitID,
					"out":    fileID,
					"impact": 0.85,
				},
			},
			"history": []interface{}{
				map[string]interface{}{
					"id":      commitID,
					"hash":    "hash123",
					"message": "fix bug",
					"date":    "2024-01-01T00:00:00Z",
					"author":  []string{"dev"},
					"issues": []interface{}{
						map[string]interface{}{
							"id":      issueID,
							"subject": "Fix critical bug",
							"status":  "Closed",
							"weight":  []float64{0.6},
						},
					},
				},
			},
		},
	}

	mockDB := &CapturingDB{
		ReturnData: map[string]interface{}{
			"FROM file_chunk": chunks,
			"<-changed":       graphResponse,
		},
	}

	svc := &Service{
		DB: mockDB,
		AI: &MockAI{}, // Reusing MockAI from search_test.go if available
	}

	// 2. Execute Search
	results, err := svc.AskProject(context.Background(), "how does main.go work?")
	if err != nil {
		t.Fatalf("AskProject failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Expected results")
	}

	result := results[0]

	// 3. Verify Context (COMMITS & ISSUES)

	// Check Related Issues
	if len(result.Context.RelatedIssues) == 0 {
		t.Fatal("Expected related issues")
	}
	if result.Context.RelatedIssues[0].ID != issueID {
		t.Errorf("Expected Issue ID %s, got %s", issueID, result.Context.RelatedIssues[0].ID)
	}
	// Verify Usage Weight Extraction
	if result.Context.RelatedIssues[0].UsageWeight != 0.6 {
		t.Errorf("Expected Issue Weight 0.6, got %f", result.Context.RelatedIssues[0].UsageWeight)
	}

	// Check Commits (Newly Exposed)
	if len(result.Context.Commits) == 0 {
		t.Fatal("Expected commits in context")
	}
	firstCommit := result.Context.Commits[0]
	if firstCommit.ID != commitID {
		t.Errorf("Expected Commit ID %s, got %s", commitID, firstCommit.ID)
	}
	// Verify Impact Extraction
	if firstCommit.Impact != 0.85 {
		t.Errorf("Expected Commit Impact 0.85, got %f", firstCommit.Impact)
	}

}
