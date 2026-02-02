package redmine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// MockDB implements db.Executor
type MockDB struct {
	CapturedQueries []string
	MockResult      interface{}
}

func (m *MockDB) Execute(sql string) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	return m.MockResult, nil
}

func (m *MockDB) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	// For now, we capture SmartQuery as well but format it nicely
	varsMap, _ := vars.(map[string]interface{})
	formatted := fmt.Sprintf("SMART: %s | VARS: %v", sql, varsMap)
	m.CapturedQueries = append(m.CapturedQueries, formatted)
	return m.MockResult, nil
}

func (m *MockDB) Close() {}

func TestIngestIssue_Integration(t *testing.T) {
	// 1. Setup Mock Redmine Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Expect GET /issues/123.json
		if !strings.Contains(r.URL.Path, "/issues/123.json") {
			t.Errorf("Unexpected request path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		resp := map[string]interface{}{
			"issue": map[string]interface{}{
				"id":      123,
				"subject": "Test Issue with 'Quote'",
				"description": "Desc\nLine 2",
				"status":  map[string]interface{}{"id": 1, "name": "New"},
				"tracker": map[string]interface{}{"id": 2, "name": "Bug"},
				"author":  map[string]interface{}{"id": 5, "name": "John Doe"},
				"created_on": "2023-01-01T12:00:00Z",
				"updated_on": "2023-01-02T12:00:00Z",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// 2. Setup Client
	client := NewClient(server.URL, "test-key")

	// 3. Setup Mock DB
	mockDB := &MockDB{}

	// 4. Run Ingest
	err := client.IngestIssue(context.Background(), mockDB, "123")
	if err != nil {
		t.Fatalf("IngestIssue failed: %v", err)
	}

	// 5. Verify Queries
	if len(mockDB.CapturedQueries) != 1 {
		t.Fatalf("Expected 1 combined DB query, got %d", len(mockDB.CapturedQueries))
	}

	query := mockDB.CapturedQueries[0]

	// Check for SQL parts
	mustContain := []string{
		"UPDATE tracker:2 SET name = $tracker_name",
		"UPDATE author:john_doe SET name = $author_name",
		"UPDATE issue:123 SET subject = $subject",
		"RELATE issue:123->part_of->tracker:2",
		"RELATE author:john_doe->authored->issue:123",
	}

	for _, s := range mustContain {
		if !strings.Contains(query, s) {
			t.Errorf("Combined query missing: %s\nGot: %s", s, query)
		}
	}

	// Check for Variable bindings
	mustContainVars := []string{
		"tracker_name:Bug",
		"author_name:John Doe",
		"subject:Test Issue with 'Quote'",
		"description:Desc\nLine 2",
		"status:New",
	}

	for _, s := range mustContainVars {
		if !strings.Contains(query, s) {
			t.Errorf("Combined query missing variable: %s\nGot: %s", s, query)
		}
	}
}
