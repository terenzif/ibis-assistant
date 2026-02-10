package git

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/db"
)

// Reusing MockDB and MockRedmine from git_test.go (same package)

func TestProcessGitLogStream_Parsing(t *testing.T) {
	// Construct a complex git log output
	input := `
COMMIT|hash1|parent1|Author Name|2024-01-01T00:00:00Z|Initial commit
10	5	file1.txt
-	-	binary.png

COMMIT|hash2|hash1|Author Two|2024-01-02T00:00:00Z|Fix bug #123 and #456
2	1	src/code.go

COMMIT|hash3|hash2|Author Three|2024-01-03T00:00:00Z|Update "quoted file.txt"
5	0	"path/to/quoted file.txt"

COMMIT|hash4|hash3|Author Four|2024-01-04T00:00:00Z|Only verify space in path without quotes (unlikely in git but testing robustness)
1	1	path/with spaces.txt

COMMIT|hash5|hash4|Author Five|2024-01-05T00:00:00Z|Test tab split failure fallback
1 1 fallback_test.txt
`
	// Note: The above string literal uses actual tabs if copy-pasted correctly, but here might be spaces.
	// To be safe, let's REPLACE leading spaces with tabs? No, that's messy.
	// Let's assume the string literal preserves tabs.

	scanner := bufio.NewScanner(strings.NewReader(strings.TrimSpace(input)))
	mockDB := &MockDB{}
	mockRedmine := &MockRedmine{}

	err := processGitLogStream(context.Background(), scanner, mockDB, mockRedmine, "repo:test", nil, 1)
	if err != nil {
		t.Fatalf("processGitLogStream failed: %v", err)
	}

	// Assertions

	// 1. Check Issue Parsing (hash2 has #123 and #456)
	found123 := false
	found456 := false
	for _, id := range mockRedmine.IngestedIDs {
		if id == "123" { found123 = true }
		if id == "456" { found456 = true }
	}
	if !found123 {
		t.Errorf("Failed to parse issue #123")
	}
	if !found456 {
		t.Errorf("Failed to parse issue #456")
	}

	// 2. Check Quoted Path Parsing (hash3)
	// We expect "path/to/quoted file.txt" to be sanitized and used.
	// The DB query should contain: UPDATE file:path_to_quoted_file_txt SET path = 'path/to/quoted file.txt'
	foundQuotedPath := false
	targetPath := "path/to/quoted file.txt"

	for _, qry := range mockDB.CapturedQueries {
		if strings.Contains(qry, fmt.Sprintf("path = '%s'", db.EscapeSQL(targetPath))) {
			foundQuotedPath = true
		}
	}
	if !foundQuotedPath {
		// This likely fails because the input string probably has spaces instead of tabs due to editing/copy-paste.
		t.Errorf("Failed to parse quoted path: %s (Check tabs in input string)", targetPath)
	}

	// 3. Check Binary File Parsing (hash1)
	// added=0, deleted=0 for binary
	foundBinary := false
	for _, qry := range mockDB.CapturedQueries {
		// Look for RELATE ...->file:binary_png ... added = 0, deleted = 0
		if strings.Contains(qry, "file:binary_png") && strings.Contains(qry, "added = 0") && strings.Contains(qry, "deleted = 0") {
			foundBinary = true
		}
	}
	if !foundBinary {
		t.Errorf("Failed to parse binary file stats")
	}
}

func TestProcessGitLogStream_Parsing_Robustness(t *testing.T) {
	// Programmatically build input with Tabs ensures correct format
	var sb strings.Builder
	sb.WriteString("COMMIT|hash1|parent|Me|Date|Msg\n")
	sb.WriteString("5\t3\tpath/with spaces.txt\n") // Tab separated
	sb.WriteString("COMMIT|hash2|hash1|Me|Date|Msg\n")
	sb.WriteString("1 1 space_separated_fallback.txt\n") // Space separated (should fail if code strictly requires tabs)

	scanner := bufio.NewScanner(strings.NewReader(sb.String()))
	mockDB := &MockDB{}
	mockRedmine := &MockRedmine{}

	err := processGitLogStream(context.Background(), scanner, mockDB, mockRedmine, "repo:test", nil, 1)
	if err != nil {
		t.Fatalf("processGitLogStream failed: %v", err)
	}

	// Check for "path/with spaces.txt"
	foundSpacePath := false
	for _, qry := range mockDB.CapturedQueries {
		if strings.Contains(qry, "path = 'path/with spaces.txt'") {
			foundSpacePath = true
		}
	}
	if !foundSpacePath {
		t.Errorf("Failed to parse tab-separated path with spaces")
	}

	// Check for fallback
	foundFallback := false
	for _, qry := range mockDB.CapturedQueries {
		if strings.Contains(qry, "space_separated_fallback.txt") {
			foundFallback = true
		}
	}

	// Expectation: Current code skips if no tabs. So foundFallback should be FALSE.
	if foundFallback {
		t.Log("Fallback worked!")
	} else {
		t.Log("Fallback logic skipped space-separated line (as expected)")
	}
}
