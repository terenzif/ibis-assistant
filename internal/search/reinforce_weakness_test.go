package search

import (
	"strings"
	"testing"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

// TestReinforcePath_AmbiguousIDs verifies that ReinforcePath does not update incorrect edge tables
// when IDs contain misleading substrings (e.g. "issue:fix_commit_bug").
func TestReinforcePath_AmbiguousIDs(t *testing.T) {
	mockDB := &MockDB{}
	svc := &Service{
		DB: mockDB,
		AI: &MockAI{},
	}

	t.Run("Issue to Issue with Commit Keyword", func(t *testing.T) {
		mockDB.SmartCalls = nil // Reset calls

		// Test Case: Issue -> Issue, but source ID contains "commit"
		// Expected behavior: No edge update query (because Issue->Issue is not supported).
		// Current buggy behavior: Updates schema.EdgeImplements because strings.Contains("commit") is true.

		sourceID := "issue:fix_commit_bug"
		targetID := "issue:123"

		err := svc.ReinforcePath(sourceID, targetID, 0.5)
		if err != nil {
			t.Fatalf("ReinforcePath failed: %v", err)
		}

		// Check if any edge update query was executed
		for _, call := range mockDB.SmartCalls {
			if strings.Contains(call, "UPDATE edge_") {
				t.Errorf("Expected NO edge update for Issue->Issue, but got: %s", call)
			}
		}

		// Access count update should still happen on target
		foundAccessUpdate := false
		for _, call := range mockDB.SmartCalls {
			if strings.Contains(call, "access_count") {
				foundAccessUpdate = true
				break
			}
		}
		if !foundAccessUpdate {
			t.Error("Expected target access_count update, but not found")
		}
	})

	t.Run("Commit to File with Issue Keyword", func(t *testing.T) {
		mockDB.SmartCalls = nil

		// Test Case: Commit -> File, but target ID contains "issue"
		// Expected: Update EdgeChanged (commit->file)
		// Buggy behavior: Might update EdgeImplements if checking "issue" first?
		// Actually, in current code, `commit` & `issue` check is first.
		// If source="commit:1" and target="file:issue_tracker.go",
		// strings.Contains(target, "issue") is true.
		// So it might try to update `edge_implements` instead of `edge_changed`.

		sourceID := "commit:1"
		targetID := "file:issue_tracker.go"

		err := svc.ReinforcePath(sourceID, targetID, 0.5)
		if err != nil {
			t.Fatalf("ReinforcePath failed: %v", err)
		}

		foundCorrectUpdate := false
		foundIncorrectUpdate := false

		for _, call := range mockDB.SmartCalls {
			if strings.Contains(call, schema.EdgeChanged) {
				foundCorrectUpdate = true
			}
			if strings.Contains(call, schema.EdgeImplements) {
				foundIncorrectUpdate = true
			}
		}

		if foundIncorrectUpdate {
			t.Errorf("Incorrectly updated %s instead of %s", schema.EdgeImplements, schema.EdgeChanged)
		}
		if !foundCorrectUpdate {
			t.Errorf("Failed to update %s", schema.EdgeChanged)
		}
	})
}
