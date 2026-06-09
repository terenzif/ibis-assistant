package jira

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/ticketing"
)

func TestGetIssueUsesBasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user@example.com:token"))
		if got := r.Header.Get("Authorization"); got != expected {
			t.Fatalf("unexpected auth header %s", got)
		}
		if !strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "100",
			"key": "ABC-100",
			"self": "http://jira/ABC-100",
			"fields": map[string]interface{}{
				"summary": "Fix login",
				"description": map[string]interface{}{"type": "doc"},
				"status": map[string]interface{}{"name": "In Progress"},
				"issuetype": map[string]interface{}{"name": "Bug"},
				"project": map[string]interface{}{"key": "ABC"},
			},
		})
	}))
	defer srv.Close()

	provider := New(ticketing.JiraConfig{BaseURL: srv.URL, Email: "system@example.com", APIToken: "system-token"})
	issue, err := provider.GetIssue(context.Background(), ticketing.AuthContext{JiraEmail: "user@example.com", JiraAPIToken: "token"}, "ABC-100", "ABC")
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}
	if issue.ExternalKey != "ABC-100" {
		t.Fatalf("expected key ABC-100, got %s", issue.ExternalKey)
	}
}

func TestSearchIssues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"total": 1,
			"startAt": 0,
			"maxResults": 20,
			"issues": []map[string]interface{}{
				{
					"id": "101",
					"key": "XYZ-101",
					"fields": map[string]interface{}{
						"summary": "Timeout",
						"description": map[string]interface{}{"type": "doc"},
						"status": map[string]interface{}{"name": "Open"},
						"issuetype": map[string]interface{}{"name": "Task"},
						"project": map[string]interface{}{"key": "XYZ"},
					},
				},
			},
		})
	}))
	defer srv.Close()

	provider := New(ticketing.JiraConfig{BaseURL: srv.URL, Email: "u", APIToken: "t"})
	result, err := provider.SearchIssues(context.Background(), ticketing.AuthContext{}, ticketing.SearchParams{ProjectKey: "XYZ", Query: "timeout"})
	if err != nil {
		t.Fatalf("SearchIssues failed: %v", err)
	}
	if result.TotalCount != 1 || len(result.Issues) != 1 {
		t.Fatalf("unexpected result %+v", result)
	}
}
