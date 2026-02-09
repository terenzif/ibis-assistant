package search

import (
	"encoding/json"
	"testing"
)

// Reusing MockDB from graph_test.go which is in the same package

func TestGetFileContext_Robustness_ScalarWeight(t *testing.T) {
	// Simulate DB returning "weight": 0.9 (scalar) instead of array
	// This happens if query is ->implements.usage_weight and only one edge exists?
	// SurrealDB behavior for edge property access might vary.

	// We construct the raw JSON bytes that represent the "history" part of the query result.
	// The key is that "issues" array contains objects where "weight" is a number.

	historyJSON := `[
		{
			"id": "commit:1",
			"hash": "abc",
			"message": "msg",
			"date": "2024-01-01",
			"author": ["dev"],
			"issues": [
				{
					"id": "issue:1",
					"subject": "bug",
					"status": "open",
					"weight": 0.9
				}
			]
		}
	]`

	var historyData interface{}
	if err := json.Unmarshal([]byte(historyJSON), &historyData); err != nil {
		t.Fatalf("Failed to setup test data: %v", err)
	}

	mockResponse := []interface{}{
		map[string]interface{}{
			"change_edges": []interface{}{}, // No impact mapping needed for this test
			"history":      historyData,
		},
	}

	mockDB := &MockDB{
		ReturnData: map[string]interface{}{
			"SELECT": mockResponse,
		},
	}

	// This call should NOT panic or return error, but parse the weight correctly if possible.
	// If the current implementation expects []float64, it will likely fail unmarshaling.
	ctx, err := GetFileContext(mockDB, "/file")
	if err != nil {
		t.Fatalf("GetFileContext failed unexpectedly: %v", err)
	}

	// Verify
	if len(ctx.Commits) != 1 {
		t.Fatalf("Expected 1 commit, got %d", len(ctx.Commits))
	}
	if len(ctx.Issues) != 1 {
		// If unmarshal failed for history, we might get 0 issues or 0 commits (if whole history failed)
		// Current impl: "if err := json.Unmarshal(bytes, &tempCommits); err != nil { return graphCtx, nil }"
		// So if unmarshal fails, we get 0 commits.
		t.Fatalf("Expected 1 issue, got %d (Unmarshaling likely failed due to scalar weight)", len(ctx.Issues))
	}

	if ctx.Issues[0].UsageWeight != 0.9 {
		t.Errorf("Expected weight 0.9, got %f", ctx.Issues[0].UsageWeight)
	}
}

func TestGetFileContext_Robustness_ArrayWeight(t *testing.T) {
	// Standard case: "weight": [0.9]
	historyJSON := `[
		{
			"id": "commit:2",
			"hash": "def",
			"message": "msg2",
			"date": "2024-01-02",
			"author": ["dev"],
			"issues": [
				{
					"id": "issue:2",
					"subject": "feature",
					"status": "closed",
					"weight": [0.8]
				}
			]
		}
	]`

	var historyData interface{}
	json.Unmarshal([]byte(historyJSON), &historyData)

	mockResponse := []interface{}{
		map[string]interface{}{
			"change_edges": []interface{}{},
			"history":      historyData,
		},
	}

	mockDB := &MockDB{
		ReturnData: map[string]interface{}{
			"SELECT": mockResponse,
		},
	}

	ctx, err := GetFileContext(mockDB, "/file")
	if err != nil {
		t.Fatalf("GetFileContext failed: %v", err)
	}

	if len(ctx.Issues) != 1 {
		t.Fatalf("Expected 1 issue, got %d", len(ctx.Issues))
	}
	if ctx.Issues[0].UsageWeight != 0.8 {
		t.Errorf("Expected weight 0.8, got %f", ctx.Issues[0].UsageWeight)
	}
}

func TestGetFileContext_Robustness_MixedTypes(t *testing.T) {
	// Test mixed types in IDs (int vs string) which is handled by existing tests,
	// but let's test "impact" edge returning unusual types?
	// The code does: `if imp, ok := em["impact"].(float64); ok`
	// If it's not float64 (e.g. int 1 or string "0.5"), it defaults to 0.0.

	// This test documents that behavior.
	mockResponse := []interface{}{
		map[string]interface{}{
			"change_edges": []interface{}{
				map[string]interface{}{
					"in": "commit:3",
					"out": "file:3",
					"impact": "high", // String, should be ignored
				},
			},
			"history": []interface{}{
				map[string]interface{}{
					"id": "commit:3",
					"hash": "ghi",
					"message": "msg3",
					"date": "2024-01-03",
				},
			},
		},
	}

	mockDB := &MockDB{
		ReturnData: map[string]interface{}{
			"SELECT": mockResponse,
		},
	}

	ctx, err := GetFileContext(mockDB, "/file")
	if err != nil {
		t.Fatal(err)
	}

	if len(ctx.Commits) == 1 {
		if ctx.Commits[0].Impact != 0.0 {
			t.Errorf("Expected impact 0.0 (ignored string), got %f", ctx.Commits[0].Impact)
		}
	}
}
