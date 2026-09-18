package azuredevops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/terenzif/ibis-assistant/internal/ticketing"
)

func TestGetIssueUsesPAT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte(":pat-token"))
		if got := r.Header.Get("Authorization"); got != expected {
			t.Fatalf("unexpected auth header %s", got)
		}
		if !strings.Contains(r.URL.Path, "/_apis/wit/workitems/") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":  123,
			"url": "http://ado/workitems/123",
			"fields": map[string]interface{}{
				"System.Title":        "Fix bug",
				"System.State":        "Resolved",
				"System.WorkItemType": "Bug",
				"System.TeamProject":  "Core",
			},
		})
	}))
	defer srv.Close()

	provider := New(ticketing.AzureDevOpsConfig{OrganizationURL: srv.URL, Project: "Core", PAT: "pat-token"})
	issue, err := provider.GetIssue(context.Background(), ticketing.AuthContext{}, "123", "Core")
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}
	if issue.ExternalID != "123" {
		t.Fatalf("expected 123, got %s", issue.ExternalID)
	}
}

func TestSearchIssuesWIQLFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/_apis/wit/wiql"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"workItems": []map[string]interface{}{{"id": 50}},
			})
		case strings.Contains(r.URL.Path, "/_apis/wit/workitems"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"value": []map[string]interface{}{
					{"id": 50, "fields": map[string]interface{}{"System.Title": "Timeout", "System.State": "Active", "System.WorkItemType": "Task", "System.TeamProject": "Core"}},
				},
			})
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	provider := New(ticketing.AzureDevOpsConfig{OrganizationURL: srv.URL, Project: "Core", PAT: "x"})
	result, err := provider.SearchIssues(context.Background(), ticketing.AuthContext{}, ticketing.SearchParams{ProjectKey: "Core", Query: "timeout"})
	if err != nil {
		t.Fatalf("SearchIssues failed: %v", err)
	}
	if len(result.Issues) != 1 || result.Issues[0].ExternalID != "50" {
		t.Fatalf("unexpected result %+v", result)
	}
}
