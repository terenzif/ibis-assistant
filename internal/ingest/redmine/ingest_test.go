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
	if len(mockDB.CapturedQueries) == 0 {
		t.Fatal("Expected DB queries, got none")
	}

	// Check for SmartQuery updates

	// Tracker
	// SMART: UPDATE tracker:2 SET name = $name; | VARS: map[name:Bug]
	foundTracker := false
	for _, q := range mockDB.CapturedQueries {
		if strings.Contains(q, "SMART: UPDATE tracker:2") && strings.Contains(q, "name:Bug") {
			foundTracker = true
			break
		}
	}
	if !foundTracker {
		t.Errorf("Missing tracker update (SmartQuery). Got: %v", mockDB.CapturedQueries)
	}

	// Author
	foundAuthor := false
	for _, q := range mockDB.CapturedQueries {
		if strings.Contains(q, "SMART: UPDATE author:john_doe") && strings.Contains(q, "name:John Doe") {
			foundAuthor = true
			break
		}
	}
	if !foundAuthor {
		t.Errorf("Missing author update (SmartQuery). Got: %v", mockDB.CapturedQueries)
	}

	// Issue
	foundIssue := false
	for _, q := range mockDB.CapturedQueries {
		if strings.Contains(q, "SMART: UPDATE issue:123") {
			// Check vars map string representation
			if strings.Contains(q, "Test Issue with 'Quote'") && strings.Contains(q, "Desc\nLine 2") {
				foundIssue = true
				break
			}
		}
	}
	if !foundIssue {
		t.Errorf("Missing issue update (SmartQuery). Got: %v", mockDB.CapturedQueries)
	}

	// Links
	// RELATE issue:123->part_of->tracker:2;
	expectedLink1 := "RELATE issue:123->part_of->tracker:2;"
	if !contains(mockDB.CapturedQueries, expectedLink1) {
		t.Errorf("Missing link issue->tracker")
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
