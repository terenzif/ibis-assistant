package search

import (
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
	ctx, err := GetFileContext(mockDB, "/path/to/file")
	if err != nil {
		t.Fatalf("GetFileContext failed: %v", err)
	}

	// Check results
	if len(ctx.Commits) != 1 {
		t.Fatalf("Expected 1 commit, got %d", len(ctx.Commits))
	}

	c := ctx.Commits[0]

	// We EXPECT the impact to be 0.8, but due to the bug (missing ID), it will be 0.0.
	// This test is expected to FAIL until we fix the code.
	if c.Impact != 0.8 {
		t.Errorf("Expected Impact 0.8, got %f", c.Impact)
	}

	if c.Hash != "hash123" {
		t.Errorf("Expected hash hash123, got %s", c.Hash)
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

		ctx, err := GetFileContext(mockDB, "/file")
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

		ctx, err := GetFileContext(mockDB, "/file")
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

		ctx, err := GetFileContext(mockDB, "/file")
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
