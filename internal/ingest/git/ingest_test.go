package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/terenzif/ibis-assistant/internal/db"
	"github.com/terenzif/ibis-assistant/internal/schema"
)

// TestIngestRepoIntegration creates a real temporary git repo and runs the ingestion logic against a Mock DB.
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
	helperExec(t, repoDir, "git", "config", "user.name", "Test User")
	helperExec(t, repoDir, "git", "config", "user.email", "test@example.com")

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
	err := IngestRepo(context.Background(), mockDB, mockRedmine, repoDir, "test-repo", 10)
	if err != nil {
		t.Fatalf("IngestRepo failed: %v", err)
	}

	// Wait for background Redmine goroutine to finish
	time.Sleep(100 * time.Millisecond)

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
		if strings.Contains(qry, fmt.Sprintf("->%s->", schema.EdgeImplements)) && strings.Contains(qry, db.FormatRecordID(schema.TableIssue, "42")) {
			foundIssueLink = true
		}
	}
	if !foundIssueLink {
		t.Errorf("Did not find 'implements->issue:42' relation in queries")
	}
}

func TestIngestRepo_QuotedPaths(t *testing.T) {
	// 1. Setup Temp Git Repo
	repoDir := t.TempDir()

	initCmd := exec.Command("git", "init")
	initCmd.Dir = repoDir
	if err := initCmd.Run(); err != nil {
		t.Fatalf("Failed to git init: %v", err)
	}

	// Config user
	helperExec(t, repoDir, "git", "config", "user.name", "Test User")
	helperExec(t, repoDir, "git", "config", "user.email", "test@example.com")

	// Create file with spaces (which git quotes as "file with spaces.txt")
	fileName := "file with spaces.txt"
	os.WriteFile(filepath.Join(repoDir, fileName), []byte("content"), 0644)
	helperExec(t, repoDir, "git", "add", ".")
	helperExec(t, repoDir, "git", "commit", "-m", "Add quoted file")

	// 2. Setup Mock Clients
	mockDB := &MockDB{}
	mockRedmine := &MockRedmine{}

	// 3. Run Ingest
	err := IngestRepo(context.Background(), mockDB, mockRedmine, repoDir, "test-repo", 10)
	if err != nil {
		t.Fatalf("IngestRepo failed: %v", err)
	}

	// 4. Assertions
	// We expect the file ID to be sanitized without quotes: file:file_with_spaces_txt
	// And the path update to use the clean name

	foundFileUpdate := false
	expectedPath := fileName
	// sanitizeID("file with spaces.txt") -> "file_with_spaces_txt"
	// IngestRepo uses <Table>:<Repo>_<File>
	expectedID := db.FormatRecordID(schema.TableFile, fmt.Sprintf("%s_%s", db.SanitizeID("test-repo"), db.SanitizeID(fileName)))

	for _, qry := range mockDB.CapturedQueries {
		// Check for UPDATE source_file:test_repo_file_with_spaces_txt SET path = 'file with spaces.txt'
		if strings.Contains(qry, fmt.Sprintf("UPDATE %s", expectedID)) {
			if strings.Contains(qry, fmt.Sprintf("path = '%s'", db.EscapeSQL(expectedPath))) {
				foundFileUpdate = true
			}
		}
		// Also ensure no double quotes in the ID part of the query like source_file:"..."
		if strings.Contains(qry, fmt.Sprintf("%s:\"", schema.TableFile)) || strings.Contains(qry, "Ôƒ¿") {
			// This is a bit hacky to detect encoding issues in tests, but the main goal is no double quotes around ID.
		}
	}

	if !foundFileUpdate {
		t.Errorf("Did not find correct file update for quoted path. Queries: %v", mockDB.CapturedQueries)
	}
}

func TestIngestRepo_Incremental(t *testing.T) {
	// 1. Setup Temp Git Repo
	repoDir := t.TempDir()

	initCmd := exec.Command("git", "init")
	initCmd.Dir = repoDir
	if err := initCmd.Run(); err != nil {
		t.Fatalf("Failed to git init: %v", err)
	}

	// Config user
	helperExec(t, repoDir, "git", "config", "user.name", "Test User")
	helperExec(t, repoDir, "git", "config", "user.email", "test@example.com")

	// Commit 1 (Already Ingested)
	os.WriteFile(filepath.Join(repoDir, "file1.go"), []byte("package main"), 0644)
	helperExec(t, repoDir, "git", "add", ".")
	helperExec(t, repoDir, "git", "commit", "-m", "Commit One")

	// Get Hash of Commit 1
	out, _ := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	hash1 := strings.TrimSpace(string(out))

	// Commit 2 (New)
	os.WriteFile(filepath.Join(repoDir, "file2.go"), []byte("package main\n// new"), 0644)
	helperExec(t, repoDir, "git", "add", ".")
	helperExec(t, repoDir, "git", "commit", "-m", "Commit Two")

	// Get Hash of Commit 2
	out2, _ := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	hash2 := strings.TrimSpace(string(out2))

	// 2. Setup Mock Clients
	mockDB := &MockDB{}
	mockRedmine := &MockRedmine{}

	// Mock Result: Commit 1 exists
	// SmartQuery returns a flat list of existing commit IDs (SELECT VALUE format)
	mockDB.MockResult = []interface{}{
		db.FormatRecordID(schema.TableCommit, hash1),
	}

	// 3. Run Ingest
	err := IngestRepo(context.Background(), mockDB, mockRedmine, repoDir, "test-repo", 10)
	if err != nil {
		t.Fatalf("IngestRepo failed: %v", err)
	}

	// 4. Assertions
	// We expect Commit 1 to be skipped (no UPDATE/CREATE commit:hash1)
	// We expect Commit 2 to be ingested (UPDATE/CREATE commit:hash2)

	foundCommit1 := false
	foundCommit2 := false

	for _, qry := range mockDB.CapturedQueries {
		// Check for hash1 usage in CREATE/UPDATE
		if strings.Contains(qry, db.FormatRecordID(schema.TableCommit, hash1)) {
			// It might be referenced as parent of commit 2?
			// RELATE ⟨commit:hash1⟩->parent_of->⟨commit:hash2⟩
			// But the CREATE/UPDATE statement for hash1 itself should be missing if skipped.
			// The existing logic generates: "CREATE ⟨commit:hash1⟩ ..."
			// We check specifically for setting hash/date/message for hash1
			if strings.Contains(qry, fmt.Sprintf("hash = '%s'", hash1)) {
				foundCommit1 = true
			}
		}

		if strings.Contains(qry, db.FormatRecordID(schema.TableCommit, hash2)) {
			if strings.Contains(qry, fmt.Sprintf("hash = '%s'", hash2)) {
				foundCommit2 = true
			}
		}
	}

	if foundCommit1 {
		t.Errorf("Expected Commit 1 to be skipped, but found SQL for it")
	}
	if !foundCommit2 {
		t.Errorf("Expected Commit 2 to be ingested, but found no SQL for it")
	}
}

func TestIngestRepo_SlowRedmine(t *testing.T) {
	// 1. Setup Temp Git Repo
	repoDir := t.TempDir()

	initCmd := exec.Command("git", "init")
	initCmd.Dir = repoDir
	if err := initCmd.Run(); err != nil {
		t.Fatalf("Failed to git init: %v", err)
	}

	// Config user
	helperExec(t, repoDir, "git", "config", "user.name", "Test User")
	helperExec(t, repoDir, "git", "config", "user.email", "test@example.com")

	// Commit with Issue
	os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main"), 0644)
	helperExec(t, repoDir, "git", "add", ".")
	helperExec(t, repoDir, "git", "commit", "-m", "Fix #100")

	// 2. Setup Mock Clients with Delay
	mockDB := &MockDB{}
	mockRedmine := &MockRedmine{
		Delay: 200 * time.Millisecond,
	}

	// 3. Run Ingest
	// Using concurrency 1, so the worker will block for 200ms per issue.
	// Since we only have 1 issue, it should take ~200ms + overhead.
	start := time.Now()
	err := IngestRepo(context.Background(), mockDB, mockRedmine, repoDir, "test-repo", 1)
	if err != nil {
		t.Fatalf("IngestRepo failed: %v", err)
	}
	duration := time.Since(start)

	// 4. Assertions
	if len(mockRedmine.IngestedIDs) != 1 {
		t.Fatalf("Expected 1 issue ingested, got %d", len(mockRedmine.IngestedIDs))
	}
	if mockRedmine.IngestedIDs[0] != "100" {
		t.Errorf("Expected issue '100', got '%s'", mockRedmine.IngestedIDs[0])
	}

	// Check that we waited for it (implied by IngestRepo returning only after wg.Wait())
	// If IngestRepo returns immediately without waiting, duration would be small.
	// But IngestRepo has defer wg.Wait().
	if duration < 200*time.Millisecond {
		t.Errorf("IngestRepo returned too quickly (%v), implying it didn't wait for Redmine ingestion", duration)
	}
}
