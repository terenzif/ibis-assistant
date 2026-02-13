package search

import (
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
		err := svc.ReinforcePath("author:1", "commit:1", 0.5)
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
		err := svc.ReinforcePath("foo:1", "bar:1", 0.5)
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
