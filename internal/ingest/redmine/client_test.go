package redmine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/auth"
)

func TestGetIssue(t *testing.T) {
	// Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check URL
		if r.URL.Path != "/issues/123.json" {
			t.Errorf("Expected path /issues/123.json, got %s", r.URL.Path)
		}
		// Check Auth Header
		if r.Header.Get("X-Redmine-API-Key") != "fake-key" {
			t.Errorf("Missing or wrong API key")
		}

		// Return Mock JSON
		resp := map[string]interface{}{
			"issue": map[string]interface{}{
				"id":      123,
				"subject": "Test Issue",
				"status":  map[string]interface{}{"id": 1, "name": "New"},
				"tracker": map[string]interface{}{"id": 2, "name": "Bug"},
				"author":  map[string]interface{}{"id": 5, "name": "John Doe"},
				"description": "Desc",
				"created_on": "2023-01-01T12:00:00Z",
				"updated_on": "2023-01-02T12:00:00Z",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "fake-key")

	issue, err := client.GetIssue(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}

	if issue.ID != 123 {
		t.Errorf("Expected ID 123, got %d", issue.ID)
	}
	if issue.Subject != "Test Issue" {
		t.Errorf("Expected Subject 'Test Issue', got %s", issue.Subject)
	}
	if issue.Author.Name != "John Doe" {
		t.Errorf("Expected Author 'John Doe', got %s", issue.Author.Name)
	}
}

func TestSearchIssues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/issues.json" {
			t.Errorf("Expected path /issues.json, got %s", r.URL.Path)
		}
		q := r.URL.Query().Get("subject")
		if q != "~login" {
			t.Errorf("Expected query ~login, got %s", q)
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("Expected limit 10, got %s", r.URL.Query().Get("limit"))
		}
		if r.URL.Query().Get("offset") != "0" {
			t.Errorf("Expected offset 0, got %s", r.URL.Query().Get("offset"))
		}

		resp := map[string]interface{}{
			"issues": []map[string]interface{}{
				{"id": 10, "subject": "Login Fix", "status": map[string]interface{}{"name":"Closed"}},
				{"id": 11, "subject": "Login UI", "status": map[string]interface{}{"name":"New"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "key")
	issues, err := client.SearchIssues(context.Background(), "login")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(issues) != 2 {
		t.Errorf("Expected 2 issues, got %d", len(issues))
	}
	if issues[0].ID != 10 {
		t.Errorf("Expected first issue ID 10, got %d", issues[0].ID)
	}
}

func TestSearchIssuesAdvanced(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/issues.json" {
			t.Errorf("Expected path /issues.json, got %s", r.URL.Path)
		}

		q := r.URL.Query()
		if q.Get("subject") != "~timeout" {
			t.Errorf("Expected subject ~timeout, got %s", q.Get("subject"))
		}
		if q.Get("project_id") != "core" {
			t.Errorf("Expected project_id core, got %s", q.Get("project_id"))
		}
		if q.Get("status_id") != "open" {
			t.Errorf("Expected status_id open, got %s", q.Get("status_id"))
		}
		if q.Get("assigned_to_id") != "me" {
			t.Errorf("Expected assigned_to_id me, got %s", q.Get("assigned_to_id"))
		}
		if q.Get("limit") != "25" {
			t.Errorf("Expected limit 25, got %s", q.Get("limit"))
		}
		if q.Get("offset") != "50" {
			t.Errorf("Expected offset 50, got %s", q.Get("offset"))
		}
		if q.Get("sort") != "updated_on:desc" {
			t.Errorf("Expected sort updated_on:desc, got %s", q.Get("sort"))
		}

		resp := map[string]interface{}{
			"total_count": 101,
			"offset":      50,
			"limit":       25,
			"issues": []map[string]interface{}{
				{"id": 123, "subject": "DB timeout on startup", "status": map[string]interface{}{"name": "In Progress"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "key")
	res, err := client.SearchIssuesAdvanced(context.Background(), SearchIssuesParams{
		Query:        "timeout",
		ProjectID:    "core",
		StatusID:     "open",
		AssignedToID: "me",
		Limit:        25,
		Offset:       50,
		Sort:         "updated_on:desc",
	})
	if err != nil {
		t.Fatalf("SearchIssuesAdvanced failed: %v", err)
	}

	if res.TotalCount != 101 {
		t.Errorf("Expected total_count 101, got %d", res.TotalCount)
	}
	if len(res.Issues) != 1 {
		t.Errorf("Expected 1 issue, got %d", len(res.Issues))
	}
}

func TestSearchMyIssuesForcesAssignedToMe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("assigned_to_id") != "me" {
			t.Errorf("Expected assigned_to_id me, got %s", r.URL.Query().Get("assigned_to_id"))
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"issues": []map[string]interface{}{}})
	}))
	defer server.Close()

	client := NewClient(server.URL, "key")
	_, err := client.SearchMyIssues(context.Background(), SearchIssuesParams{AssignedToID: "42"})
	if err != nil {
		t.Fatalf("SearchMyIssues failed: %v", err)
	}
}

func TestValidateSearchParams(t *testing.T) {
	if err := ValidateSearchParams(SearchIssuesParams{Limit: 101}); err == nil {
		t.Fatalf("expected error for limit > 100")
	}
	if err := ValidateSearchParams(SearchIssuesParams{Offset: -1}); err == nil {
		t.Fatalf("expected error for negative offset")
	}
	if err := ValidateSearchParams(SearchIssuesParams{Sort: "bad:desc"}); err == nil {
		t.Fatalf("expected error for invalid sort")
	}
	if err := ValidateSearchParams(SearchIssuesParams{UpdatedFrom: "2026/01/01"}); err == nil {
		t.Fatalf("expected error for invalid date")
	}
	if err := ValidateSearchParams(SearchIssuesParams{Sort: "updated_on:desc", UpdatedFrom: "2026-01-01"}); err != nil {
		t.Fatalf("expected valid params, got error: %v", err)
	}
}

func TestUpdateIssueWithExtendedFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("Expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/issues/77.json" {
			t.Errorf("Expected /issues/77.json, got %s", r.URL.Path)
		}

		body, _ := io.ReadAll(r.Body)
		defer r.Body.Close()

		if !bytes.Contains(body, []byte(`"notes":"working on fix"`)) {
			t.Errorf("Expected notes in payload, got %s", string(body))
		}
		if !bytes.Contains(body, []byte(`"status_id":2`)) {
			t.Errorf("Expected status_id in payload, got %s", string(body))
		}
		if !bytes.Contains(body, []byte(`"priority_id":3`)) {
			t.Errorf("Expected priority_id in payload, got %s", string(body))
		}
		if !bytes.Contains(body, []byte(`"assigned_to_id":15`)) {
			t.Errorf("Expected assigned_to_id in payload, got %s", string(body))
		}
		if !bytes.Contains(body, []byte(`"fixed_version_id":8`)) {
			t.Errorf("Expected fixed_version_id in payload, got %s", string(body))
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(server.URL, "key")
	err := client.UpdateIssue(context.Background(), "77", UpdateIssueParams{
		Notes:          "working on fix",
		StatusID:       2,
		PriorityID:     3,
		AssignedToID:   15,
		FixedVersionID: 8,
	})
	if err != nil {
		t.Fatalf("UpdateIssue failed: %v", err)
	}
}

func TestUpdateIssueRequiresAtLeastOneField(t *testing.T) {
	client := NewClient("http://example.com", "key")
	err := client.UpdateIssue(context.Background(), "1", UpdateIssueParams{})
	if err == nil {
		t.Fatalf("expected error when no update fields are provided")
	}
}

func TestContextKeyOverride(t *testing.T) {
	// Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify we got the User Key, not the System Key
		if r.Header.Get("X-Redmine-API-Key") != "user-secret-key" {
			t.Errorf("Expected user-secret-key, got %s", r.Header.Get("X-Redmine-API-Key"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"issue": {"id": 999}}`))
	}))
	defer server.Close()

	// Client has System Key
	client := NewClient(server.URL, "system-key")

	// Context has User Key
	ctx := context.WithValue(context.Background(), auth.RedmineKeyContextKey, "user-secret-key")

	_, err := client.GetIssue(ctx, "999")
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}
}

func TestClientTimeout(t *testing.T) {
	client := NewClient("http://example.com", "key")
	if client.HTTP.Timeout != 30*time.Second {
		t.Errorf("Expected timeout to be 30s, got %v", client.HTTP.Timeout)
	}
}

func TestClientURLTrimming(t *testing.T) {
	client := NewClient("http://example.com/", "key")
	if client.BaseURL != "http://example.com" {
		t.Errorf("Expected BaseURL to be 'http://example.com', got '%s'", client.BaseURL)
	}
}
