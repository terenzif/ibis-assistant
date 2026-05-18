package search

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Reusing MockDB from search_test.go (it's in the same package)

func TestGetFileContext_Success(t *testing.T) {
	// Verify that GetFileContext correctly maps impact using the ID
	// returned by the query.

	// Mock Data
	// Commit ID is "commit:123"
	// change_edges has "in": "commit:123", "impact": 0.8
	// history item has NO "id" field, only hash, etc.

	mockResponse := []interface{}{
		map[string]interface{}{
			"change_edges": []interface{}{
				map[string]interface{}{
					"in":     "commit:123",
					"out":    "file:abc",
					"impact": 0.8,
				},
			},
			"history": []interface{}{
				map[string]interface{}{
					// "id" is now present as requested by the fixed query
					"id":      "commit:123",
					"hash":    "hash123",
					"message": "fix bug",
					"date":    "2024-01-01T00:00:00Z",
					"author":  []string{"dev"},
					"issues":  []interface{}{},
				},
			},
		},
	}

	mockDB := &MockDB{
		ReturnData: map[string]interface{}{
			"SELECT": mockResponse, // The mockDB matches by prefix "SELECT"
		},
	}

	// Call GetFileContext
	ctx, err := GetFileContext(context.Background(), mockDB, "/path/to/file")
	if err != nil {
		t.Fatalf("GetFileContext failed: %v", err)
	}

	// Check results
	if len(ctx.Commits) != 1 {
		t.Fatalf("Expected 1 commit, got %d", len(ctx.Commits))
	}

	c := ctx.Commits[0]

	// Impact should be correctly mapped using the ID
	if c.Impact != 0.8 {
		t.Errorf("Expected Impact 0.8, got %f", c.Impact)
	}

	if c.Hash != "hash123" {
		t.Errorf("Expected hash hash123, got %s", c.Hash)
	}

	// Verify Query Structure to prevent regression of "missing ID"
	if len(mockDB.SmartCalls) > 0 {
		query := mockDB.SmartCalls[0]
		// We expect the query to select 'id' from commit
		if !strings.Contains(query, "id,") && !strings.Contains(query, "id\n") {
			t.Errorf("Query MUST select 'id' field to map impact correctly. Got: %s", query)
		}
	}
}

func TestGetFileContext_RobustID(t *testing.T) {
	// Verify that GetFileContext handles non-string IDs robustly.

	// Using int ID for 'in' and 'id' to simulate non-string types (like RecordID objects)
	mockResponse := []interface{}{
		map[string]interface{}{
			"change_edges": []interface{}{
				map[string]interface{}{
					"in":     123, // int instead of string
					"out":    "file:abc",
					"impact": 0.8,
				},
			},
			"history": []interface{}{
				map[string]interface{}{
					"id":      123, // int instead of string
					"hash":    "hash123",
					"message": "fix bug",
					"date":    "2024-01-01T00:00:00Z",
					"author":  []string{"dev"},
					"issues":  []interface{}{},
				},
			},
		},
	}

	mockDB := &MockDB{
		ReturnData: map[string]interface{}{
			"SELECT": mockResponse,
		},
	}

	ctx, err := GetFileContext(context.Background(), mockDB, "/path/to/file")
	if err != nil {
		t.Fatalf("GetFileContext failed: %v", err)
	}

	if len(ctx.Commits) != 1 {
		t.Fatalf("Expected 1 commit, got %d", len(ctx.Commits))
	}

	c := ctx.Commits[0]

	// Impact should be correctly mapped using the converted ID "123"
	if c.Impact != 0.8 {
		t.Errorf("Expected Impact 0.8, got %f", c.Impact)
	}

	if c.ID != "123" {
		t.Errorf("Expected ID '123', got '%v'", c.ID)
	}
}

func TestGetFileContext_EdgeCases(t *testing.T) {
	t.Run("Empty History", func(t *testing.T) {
		mockResponse := []interface{}{
			map[string]interface{}{
				"change_edges": []interface{}{},
				"history":      []interface{}{},
			},
		}
		mockDB := &MockDB{
			ReturnData: map[string]interface{}{
				"SELECT": mockResponse,
			},
		}

		ctx, err := GetFileContext(context.Background(), mockDB, "/file")
		if err != nil {
			t.Fatalf("GetFileContext failed: %v", err)
		}
		if len(ctx.Commits) != 0 {
			t.Errorf("Expected 0 commits, got %d", len(ctx.Commits))
		}
	})

	t.Run("No Issues", func(t *testing.T) {
		mockResponse := []interface{}{
			map[string]interface{}{
				"change_edges": []interface{}{},
				"history": []interface{}{
					map[string]interface{}{
						"id":      "commit:1",
						"hash":    "abc",
						"message": "msg",
						"date":    "2024-01-01",
						"author":  []string{},
						"issues":  []interface{}{},
					},
				},
			},
		}
		mockDB := &MockDB{
			ReturnData: map[string]interface{}{
				"SELECT": mockResponse,
			},
		}

		ctx, err := GetFileContext(context.Background(), mockDB, "/file")
		if err != nil {
			t.Fatalf("GetFileContext failed: %v", err)
		}
		if len(ctx.Commits) != 1 {
			t.Fatalf("Expected 1 commit, got %d", len(ctx.Commits))
		}
		if len(ctx.Issues) != 0 {
			t.Errorf("Expected 0 issues, got %d", len(ctx.Issues))
		}
		if ctx.Commits[0].Author != "Unknown" {
			t.Errorf("Expected Unknown author, got %s", ctx.Commits[0].Author)
		}
	})

	t.Run("Malformed JSON", func(t *testing.T) {
		// MockDB returns nil or invalid structure
		mockDB := &MockDB{
			ReturnData: map[string]interface{}{
				"SELECT": "not-an-array",
			},
		}
		// Based on current implementation, it might not return error but empty context, or error depending on cast.
		// "rows, ok := res.([]interface{})"

		ctx, err := GetFileContext(context.Background(), mockDB, "/file")
		if err != nil {
			// It might not return error, just empty.
			// The code says "if !ok ... return graphCtx, nil"
		}

		if ctx == nil {
			t.Fatal("Context should not be nil")
		}
		if len(ctx.Commits) != 0 {
			t.Errorf("Expected 0 commits, got %d", len(ctx.Commits))
		}
	})
}

// MockID simulates a database type (like RecordID) that has different representations
// when formatted with %v versus when marshaled to JSON.
type mockID string

func (m mockID) MarshalJSON() ([]byte, error) {
	// Simulates marshaling to a string ID
	return json.Marshal("commit:" + string(m))
}

// Ensure it DOES NOT implement fmt.Stringer to rely on default %v behavior (or implement it if needed to verify)
// In this case, we rely on the fact that the underlying type is 'string', so %v prints the string content "123".
// But MarshalJSON adds "commit:" prefix.
// So %v -> "123", JSON -> "commit:123".

func TestGetFileContext_IDMismatch(t *testing.T) {
	// This test reproduces the bug where 'in' ID from change_edges (DB driver type)
	// mismatches the ID from history (parsed via JSON).

	// 'in' is mockID("123").
	// fmt.Sprintf("%v", in) -> "123".
	// history ID is mockID("123").
	// json.Marshal(history) -> "commit:123".
	// tempCommit.ID -> "commit:123".
	// Mismatch: "123" != "commit:123".

	mID := mockID("123")

	mockResponse := []interface{}{
		map[string]interface{}{
			"change_edges": []interface{}{
				map[string]interface{}{
					"in":     mID,
					"out":    "file:abc",
					"impact": 0.8,
				},
			},
			"history": []interface{}{
				map[string]interface{}{
					"id":      mID,
					"hash":    "hash123",
					"message": "fix bug",
					"date":    "2024-01-01T00:00:00Z",
					"author":  []string{"dev"},
					"issues":  []interface{}{},
				},
			},
		},
	}

	mockDB := &MockDB{
		ReturnData: map[string]interface{}{
			"SELECT": mockResponse,
		},
	}

	ctx, err := GetFileContext(context.Background(), mockDB, "/path/to/file")
	if err != nil {
		t.Fatalf("GetFileContext failed: %v", err)
	}

	if len(ctx.Commits) != 1 {
		t.Fatalf("Expected 1 commit, got %d", len(ctx.Commits))
	}

	c := ctx.Commits[0]

	// We expect the impact to be 0.8.
	// The code should now correctly normalize the ID so that 'in' ID matches 'history' ID.
	if c.Impact != 0.8 {
		t.Errorf("Expected Impact 0.8, got %f", c.Impact)
	}

	if c.ID != "commit:123" {
		t.Errorf("Expected ID 'commit:123', got '%s'", c.ID)
	}
}

