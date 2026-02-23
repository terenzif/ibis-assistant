package search

import (
	"strings"
	"testing"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

// TestReinforcePath_Weakness_IDParsing verifies that ReinforcePath does NOT update edge weights
// if the ID contains the keyword but is not the correct table.
// This test exposes the weakness of using strings.Contains for table identification.
func TestReinforcePath_Weakness_IDParsing(t *testing.T) {
	mockDB := &MockDB{}
	svc := &Service{
		DB: mockDB,
		AI: &MockAI{},
	}

	// 1. Weakness Test Case: "pro_committer:1" -> "issue:1"
	// "pro_committer" contains "commit", so strings.Contains(source, "commit") is true.
	// It should NOT update 'implements' because the source table is not 'commit'.

	sourceID := "pro_committer:1"
	targetID := "issue:1"

	err := svc.ReinforcePath(sourceID, targetID, 0.5)
	if err != nil {
		t.Fatalf("ReinforcePath failed unexpectedly: %v", err)
	}

	// Check if it incorrectly attempted to update 'implements'
	if len(mockDB.SmartCalls) > 0 {
		for _, call := range mockDB.SmartCalls {
			if strings.Contains(call, schema.EdgeImplements) {
				t.Errorf("WEAKNESS DETECTED: ReinforcePath updated '%s' for source '%s'. It matched 'commit' inside '%s'.", schema.EdgeImplements, sourceID, sourceID)
			}
		}
	}

	// 2. Weakness Test Case: "author_comment:1" -> "commit:1"
	// "author_comment" contains "author", so strings.Contains(source, "author") is true.
	// It should NOT update 'authored' because the source table is not 'author'.

	mockDB.SmartCalls = nil // Reset
	sourceID = "author_comment:1"
	targetID = "commit:1"

	err = svc.ReinforcePath(sourceID, targetID, 0.5)
	if err != nil {
		t.Fatalf("ReinforcePath failed unexpectedly: %v", err)
	}

	if len(mockDB.SmartCalls) > 0 {
		for _, call := range mockDB.SmartCalls {
			if strings.Contains(call, schema.EdgeAuthored) {
				t.Errorf("WEAKNESS DETECTED: ReinforcePath updated '%s' for source '%s'. It matched 'author' inside '%s'.", schema.EdgeAuthored, sourceID, sourceID)
			}
		}
	}
}
