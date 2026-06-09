package azuredevops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/repopr"
)

func TestCreatePR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte(":pat"))
		if got := r.Header.Get("Authorization"); got != expected {
			t.Fatalf("unexpected auth %s", got)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/pullrequests") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"pullRequestId": 88,
			"title": "My PR",
			"status": "active",
			"url": "http://ado/pr/88",
		})
	}))
	defer srv.Close()

	provider := New(Config{OrganizationURL: srv.URL, Project: "core", Repository: "repo", PAT: "pat"})
	pr, err := provider.CreatePR(context.Background(), repopr.AuthContext{}, repopr.CreateParams{SourceBranch: "feature/x", TargetBranch: "main", Title: "My PR"})
	if err != nil {
		t.Fatalf("CreatePR failed: %v", err)
	}
	if pr.ID != "88" {
		t.Fatalf("expected PR ID 88, got %s", pr.ID)
	}
}

func TestCompletePR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("expected PATCH, got %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"pullRequestId": 88,
			"title": "My PR",
			"status": "completed",
		})
	}))
	defer srv.Close()

	provider := New(Config{OrganizationURL: srv.URL, Project: "core", Repository: "repo", PAT: "pat"})
	pr, err := provider.CompletePR(context.Background(), repopr.AuthContext{}, repopr.CompleteParams{PRID: "88"})
	if err != nil {
		t.Fatalf("CompletePR failed: %v", err)
	}
	if pr.Status != "completed" {
		t.Fatalf("expected completed status, got %s", pr.Status)
	}
}
