package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan(t *testing.T) {
	// Create a temp workspace
	tempDir := t.TempDir()

	// Structure:
	// /repo1/.git/
	// /repo1/code.go
	// /repo1/node_modules/ (should be skipped by default exclude)
	// /repo1/vendor/ (should be skipped by gitignore)
	// /not_repo/
	// /repo2/.git/

	repo1 := filepath.Join(tempDir, "repo1")
	os.MkdirAll(filepath.Join(repo1, ".git"), 0755)
	os.MkdirAll(filepath.Join(repo1, "node_modules"), 0755)
	os.MkdirAll(filepath.Join(repo1, "vendor"), 0755)
	
	notRepo := filepath.Join(tempDir, "not_repo")
	os.MkdirAll(notRepo, 0755)

	repo2 := filepath.Join(tempDir, "repo2")
	os.MkdirAll(filepath.Join(repo2, ".git"), 0755)

	// Create .gitignore in repo1
	os.WriteFile(filepath.Join(repo1, ".gitignore"), []byte("vendor\n"), 0644)

	// Create a root .gitignore to test root filtering
	// Exclude "ignored_repo" if it existed
	os.WriteFile(filepath.Join(tempDir, ".gitignore"), []byte("ignored_repo\n"), 0644)
	ignoredRepo := filepath.Join(tempDir, "ignored_repo")
	os.MkdirAll(filepath.Join(ignoredRepo, ".git"), 0755)

	// Run Scanner
	scanner := NewScanner(tempDir)
	repos, err := scanner.Scan()
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Expectations:
	// Should find repo1, repo2
	// Should NOT find ignored_repo (via root gitignore)
	// Inside repo1, we can't easily assert skipped folders via output list (since it returns repos),
	// but we can trust logic if 'ignored_repo' works.
	
	foundRepo1 := false
	foundRepo2 := false
	foundIgnored := false

	for _, r := range repos {
		if r == repo1 { foundRepo1 = true }
		if r == repo2 { foundRepo2 = true }
		if r == ignoredRepo { foundIgnored = true }
	}

	if !foundRepo1 {
		t.Errorf("Failed to find repo1")
	}
	if !foundRepo2 {
		t.Errorf("Failed to find repo2")
	}
	if foundIgnored {
		t.Errorf("Found ignored_repo despite .gitignore")
	}
}

func TestIgnoreMatcher(t *testing.T) {
	matcher := &IgnoreMatcher{patterns: []string{"node_modules", "*.log", "build/"}}

	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"node_modules", true, true},
		{"src", true, false},
		{"error.log", false, true},
		{"build", true, true}, // build/ matches directory build
		{"build", false, false}, // build/ should only match directory? 
		// Actually my implementation of IgnoreMatcher logic:
		// if onlyDir && !isDir -> continue.
		// "build/" -> onlyDir=true. 
		// So file "build" should return false. Correct.
	}

	for _, tt := range tests {
		got := matcher.Match(tt.path, tt.isDir)
		if got != tt.want {
			t.Errorf("Match(%q, %v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
		}
	}
}
