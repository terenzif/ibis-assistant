package redmine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/terenzif/ibis-arc/internal/ticketing"
)

func TestProviderGetIssueUsesHeaderOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Redmine-API-Key"); got != "user-key" {
			t.Fatalf("expected user-key, got %s", got)
		}
		if r.URL.Path != "/issues/42.json" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"issue": map[string]interface{}{
				"id":      42,
				"subject": "Fix startup",
				"status":  map[string]interface{}{"id": 1, "name": "New"},
				"tracker": map[string]interface{}{"id": 2, "name": "Bug"},
				"author":  map[string]interface{}{"id": 3, "name": "Dev"},
			},
		})
	}))
	defer srv.Close()

	provider := New(ticketing.RedmineConfig{BaseURL: srv.URL, APIKey: "system-key"})
	issue, err := provider.GetIssue(context.Background(), ticketing.AuthContext{RedmineAPIKey: "user-key"}, "42", "core")
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}
	if issue.ExternalID != "42" {
		t.Fatalf("expected external id 42, got %s", issue.ExternalID)
	}
}

func TestProviderSearchIssues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"total_count": 1,
			"offset":      0,
			"limit":       20,
			"issues": []map[string]interface{}{
				{"id": 12, "subject": "Timeout", "status": map[string]interface{}{"name": "In Progress"}, "tracker": map[string]interface{}{"name": "Bug"}, "author": map[string]interface{}{"name": "Dev"}},
			},
		})
	}))
	defer srv.Close()

	provider := New(ticketing.RedmineConfig{BaseURL: srv.URL, APIKey: "system-key"})
	result, err := provider.SearchIssues(context.Background(), ticketing.AuthContext{}, ticketing.SearchParams{Query: "timeout"})
	if err != nil {
		t.Fatalf("SearchIssues failed: %v", err)
	}
	if result.TotalCount != 1 || len(result.Issues) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}
