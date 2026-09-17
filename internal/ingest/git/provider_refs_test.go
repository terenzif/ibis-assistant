package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/terenzif/ibis-server/internal/ticketing"
)

func TestIngestRepo_JiraReferenceUsesProviderScopedIssueID(t *testing.T) {
	repoDir := t.TempDir()
	helperExec(t, repoDir, "git", "init")
	helperExec(t, repoDir, "git", "config", "user.name", "Test User")
	helperExec(t, repoDir, "git", "config", "user.email", "test@example.com")

	_ = os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main"), 0644)
	helperExec(t, repoDir, "git", "add", ".")
	helperExec(t, repoDir, "git", "commit", "-m", "Fix ABC-321 timeout")

	mockDB := &MockDB{}
	mockIngester := &MockRedmine{}
	err := IngestRepo(context.Background(), mockDB, mockIngester, repoDir, "test-repo", 5)
	if err != nil {
		t.Fatalf("IngestRepo failed: %v", err)
	}

	targetIssueID := ticketing.IssueRecordID(ticketing.ProviderJira, "ABC-321")
	found := false
	for _, q := range mockDB.CapturedQueries {
		if strings.Contains(q, targetIssueID) && strings.Contains(q, "->implements->") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected implements relation to provider-scoped issue id %s", targetIssueID)
	}

	if len(mockIngester.IngestedRefs) != 1 {
		t.Fatalf("expected one ingested ref, got %d", len(mockIngester.IngestedRefs))
	}
	if mockIngester.IngestedRefs[0].Provider != ticketing.ProviderJira {
		t.Fatalf("expected jira provider, got %s", mockIngester.IngestedRefs[0].Provider)
	}
}
