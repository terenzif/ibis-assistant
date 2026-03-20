package search

import (
	"context"
	"strings"
	"testing"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

// TestReinforcePath_EdgeCases verifies ReinforcePath behavior for various edge types.
func TestReinforcePath_EdgeCases(t *testing.T) {
	mockDB := &MockDB{}
	svc := &Service{
		DB: mockDB,
		AI: &MockAI{},
	}

	t.Run("Authored Edge (Supported)", func(t *testing.T) {
		mockDB.SmartCalls = nil // Reset calls

		// Test Case: "Authored Edge" (author -> commit)
		// Expected behavior: Edge update query (usage_weight) AND target access count update.
		err := svc.ReinforcePath(context.Background(), "author:1", "commit:1", 0.5)
		if err != nil {
			t.Fatalf("ReinforcePath failed: %v", err)
		}

		if len(mockDB.SmartCalls) != 2 {
			t.Errorf("Expected 2 SmartCalls, got %d: %v", len(mockDB.SmartCalls), mockDB.SmartCalls)
		} else {
			// Check for edge update
			if !strings.Contains(mockDB.SmartCalls[0], schema.EdgeAuthored) {
				t.Errorf("Expected call 1 to update %s, got %s", schema.EdgeAuthored, mockDB.SmartCalls[0])
			}
			if !strings.Contains(mockDB.SmartCalls[0], "usage_weight") {
				t.Errorf("Expected call 1 to update usage_weight, got %s", mockDB.SmartCalls[0])
			}

			// Check for access count update
			if !strings.Contains(mockDB.SmartCalls[1], "access_count") {
				t.Errorf("Expected call 2 to update access_count, got %s", mockDB.SmartCalls[1])
			}
		}
	})

	t.Run("Unknown Edge (Unsupported)", func(t *testing.T) {
		mockDB.SmartCalls = nil // Reset calls

		// Test Case: "Truly Unknown Edge" (e.g. foo -> bar)
		// Expected behavior: No edge update query, ONLY target access count update.
		err := svc.ReinforcePath(context.Background(), "foo:1", "bar:1", 0.5)
		if err != nil {
			t.Fatalf("ReinforcePath failed: %v", err)
		}

		if len(mockDB.SmartCalls) != 1 {
			t.Errorf("Expected 1 SmartCall (access count only), got %d: %v", len(mockDB.SmartCalls), mockDB.SmartCalls)
		} else {
			if !strings.Contains(mockDB.SmartCalls[0], "access_count") {
				t.Errorf("Expected call to update access_count, got %s", mockDB.SmartCalls[0])
			}
			if strings.Contains(mockDB.SmartCalls[0], "usage_weight") {
				t.Errorf("Did not expect edge weight update for unknown edge type, got %s", mockDB.SmartCalls[0])
			}
		}
	})
}

// TestReinforcePath_AmbiguousID verifies that IDs with misleading substrings (e.g. "file:my_issue.go")
// are correctly parsed by their table prefix, preventing misidentified edge updates.
func TestReinforcePath_AmbiguousID(t *testing.T) {
	mockDB := &MockDB{}
	svc := &Service{
		DB: mockDB,
		AI: &MockAI{},
	}

	// Scenario: A file path contains the word "issue".
	// ID convention: table:id
	// source: commit:1
	// target: source_file:my_issue.go
	// This represents a "changed" edge (commit -> file).

	sourceID := schema.TableCommit + ":1"
	targetID := schema.TableFile + ":my_issue.go"

	err := svc.ReinforcePath(context.Background(), sourceID, targetID, 0.5)
	if err != nil {
		t.Fatalf("ReinforcePath failed: %v", err)
	}

	if len(mockDB.SmartCalls) == 0 {
		t.Fatal("Expected SmartCalls, got 0")
	}

	// We expect usage_weight update on 'changed' edge.
	updateQuery := mockDB.SmartCalls[0]

	// Check if it targeted the correct table
	if strings.Contains(updateQuery, schema.EdgeImplements) {
		t.Errorf("Incorrectly targeted 'implements' edge for file with 'issue' in name. Query: %s", updateQuery)
	}

	if !strings.Contains(updateQuery, schema.EdgeChanged) {
		t.Errorf("Expected update on 'changed' edge, got query: %s", updateQuery)
	}
}
