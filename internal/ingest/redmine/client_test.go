package redmine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
