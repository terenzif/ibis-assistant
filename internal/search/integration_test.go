package search

import (
	"context"
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/schema"
)

// TestSearchLifecycle simulates the flow of searching for a file and then reinforcing the result.
// It verifies that the IDs returned by the search (mocked) are correctly propagated to the reinforcement logic.
func TestSearchLifecycle(t *testing.T) {
	// Mock Data
	fileID := "file:path_to_file_go"
	commitID := "commit:hash123"
	issueID := "issue:42"

	// Mock response for AskProject -> Vector Search (GetSimilarChunks)
	chunks := []map[string]interface{}{
		{
			"id":      "chunk:1",
			"path":    "/path/to/file.go", // This will trigger GetFileContext
			"content": "package main",
			"score":   0.9,
		},
	}

	// Mock response for AskProject -> GetFileContext (Graph Query)
	// We need to return the structure expected by GetFileContext
	graphResponse := []interface{}{
		map[string]interface{}{
			"change_edges": []interface{}{
				map[string]interface{}{
					"in":     commitID,
					"out":    fileID, // Technically not used by logic but good for completeness
					"impact": 0.75,
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
							"weight":  []float64{0.5},
						},
					},
				},
			},
		},
	}

	mockDB := &MockDB{
		ReturnData: map[string]interface{}{
			"FROM file_chunk": chunks,        // For Vector Search
			"<-changed":       graphResponse, // For Graph Context
		},
	}

	svc := &Service{
		DB: mockDB,
		AI: &MockAI{},
	}

	// Step 1: User asks a question
	results, err := svc.AskProject(context.Background(), "how does file.go work?")
	if err != nil {
		t.Fatalf("AskProject failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Expected results")
	}

	result := results[0]
	if result.Path != "/path/to/file.go" {
		t.Errorf("Expected path /path/to/file.go, got %s", result.Path)
	}

	// Verify Context
	if len(result.Context.RelatedIssues) == 0 {
		t.Fatal("Expected related issues")
	}

	// Step 2: Extract IDs for Reinforcement
	// The IssueSummary struct has ID field.
	foundIssueID := result.Context.RelatedIssues[0].ID
	if foundIssueID != issueID {
		t.Errorf("Expected Issue ID %s, got %s", issueID, foundIssueID)
	}

	// Step 3: User provides feedback (Positive reinforcement)
	// We reinforce the path from Commit -> Issue
	err = svc.ReinforcePath(context.Background(), commitID, foundIssueID, 1.0)
	if err != nil {
		t.Fatalf("ReinforcePath failed: %v", err)
	}

	// Step 4: Verify Database Updates
	// We expect updates to:
	// 1. The edge (implements)
	// 2. The target node (issue)

	if len(mockDB.SmartCalls) < 2 {
		t.Errorf("Expected at least 2 DB updates, got %d", len(mockDB.SmartCalls))
	}

	// Check Edge Update
	edgeUpdateFound := false
	for i, sql := range mockDB.SmartCalls {
		if strings.Contains(sql, "UPDATE "+schema.EdgeImplements) {
			// Check vars
			vars, ok := mockDB.SmartVars[i].(map[string]interface{})
			if ok && vars["source"] == commitID && vars["target"] == issueID {
				edgeUpdateFound = true
			}
		}
	}
	if !edgeUpdateFound {
		t.Error("Did not find correct edge update query with correct variables")
	}

	// Check Node Update
	nodeUpdateFound := false
	for i, sql := range mockDB.SmartCalls {
		if strings.Contains(sql, "UPDATE $target SET access_count") {
			vars, ok := mockDB.SmartVars[i].(map[string]interface{})
			if ok && vars["target"] == issueID {
				nodeUpdateFound = true
			}
		}
	}
	if !nodeUpdateFound {
		t.Error("Did not find correct node update query with correct variables")
	}
}
