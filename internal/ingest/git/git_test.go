package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/db"
)

// MockDBClient for Git Ingestion
type MockDB struct {
	CapturedQueries []string
	MockResult      interface{}
}

func (m *MockDB) Execute(sql string) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	return m.MockResult, nil
}
func (m *MockDB) Close() {}
func (m *MockDB) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	return nil, nil
}

// MockRedmineIngester for Git Ingestion
type MockRedmine struct {
	IngestedIDs []string
}
func (m *MockRedmine) IngestIssue(dbClient db.Executor, issueIDStr string) error {
	m.IngestedIDs = append(m.IngestedIDs, issueIDStr)
	return nil
}


// TestIngestRepo creates a real temporary git repo and runs the ingestion logic against a Mock DB.
// This validates the integration between "git log" parsing and the DB calls.
func TestIngestRepoIntegration(t *testing.T) {
	// 1. Setup Temp Git Repo
	repoDir := t.TempDir()
	
	initCmd := exec.Command("git", "init")
	initCmd.Dir = repoDir
	if err := initCmd.Run(); err != nil {
		t.Fatalf("Failed to git init: %v", err)
	}
	
	// Config user (for CI usage)
	cfgName := exec.Command("git", "config", "user.name", "Test User")
	cfgName.Dir = repoDir
	cfgName.Run()
	cfgEmail := exec.Command("git", "config", "user.email", "test@example.com")
	cfgEmail.Dir = repoDir
	cfgEmail.Run()

	// Commit 1
	os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main"), 0644)
	helperExec(t, repoDir, "git", "add", ".")
	helperExec(t, repoDir, "git", "commit", "-m", "Initial commit")

	// Commit 2 (Reference Issue)
	os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n// update"), 0644)
	helperExec(t, repoDir, "git", "add", ".")
	helperExec(t, repoDir, "git", "commit", "-m", "Fix bug #42")

	// 2. Setup Mock Clients
	mockDB := &MockDB{}
	mockRedmine := &MockRedmine{}

	// 3. Run Ingest
	err := IngestRepo(mockDB, mockRedmine, repoDir)
	if err != nil {
		t.Fatalf("IngestRepo failed: %v", err)
	}

	// 4. Assertions
	// Check Redmine trigger
	if len(mockRedmine.IngestedIDs) != 1 {
		t.Errorf("Expected 1 redmine ingestion trigger, got %d", len(mockRedmine.IngestedIDs))
	} else if mockRedmine.IngestedIDs[0] != "42" {
		t.Errorf("Expected issue '42', got '%s'", mockRedmine.IngestedIDs[0])
	}
	
	// Check DB writes (Rough checks)
	// We expect Repo update, Author upserts, Commits, Links...
	if len(mockDB.CapturedQueries) == 0 {
		t.Errorf("Expected DB queries, got 0")
	}
	
	foundIssueLink := false
	for _, qry := range mockDB.CapturedQueries {
		if strings.Contains(qry, "implements->issue:42") {
			foundIssueLink = true
		}
	}
	if !foundIssueLink {
		t.Errorf("Did not find 'implements->issue:42' relation in queries")
	}
}

func helperExec(t *testing.T, dir, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Command %s %v failed: %v", name, args, err)
	}
}
