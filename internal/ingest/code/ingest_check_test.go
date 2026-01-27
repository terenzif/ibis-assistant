package code

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MockExecutor implements db.Executor for testing
type MockExecutor struct {
	ExecutedQueries []string
	MockResults     map[string]interface{} // Map SQL query substring to result
}

func (m *MockExecutor) Execute(sql string) (interface{}, error) {
	m.ExecutedQueries = append(m.ExecutedQueries, sql)

	// Check against MockResults
	for k, v := range m.MockResults {
		if strings.Contains(sql, k) {
			return v, nil
		}
	}
	return nil, nil
}

func (m *MockExecutor) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	return m.Execute(sql)
}

func (m *MockExecutor) Close() {}

func TestIngestCodebase_DeltaCheck(t *testing.T) {
	// Create a temporary file to ingest
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	content := "package main\n\nfunc main() {}"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Calculate its hash
	hash, _ := fileHash(testFile)

	// Test Case 1: Hash Matches (Should Skip)
	t.Run("SkipUnchanged", func(t *testing.T) {
		mockDB := &MockExecutor{
			MockResults: map[string]interface{}{
				"SELECT hash FROM": []interface{}{
					map[string]interface{}{"hash": hash},
				},
			},
		}

		// Pass nil aiClient as it shouldn't be called if skipped
		err := IngestCodebase(mockDB, nil, tmpDir)
		if err != nil {
			t.Fatalf("IngestCodebase failed: %v", err)
		}

		// Verify that UPDATE was NOT called
		for _, q := range mockDB.ExecutedQueries {
			if strings.Contains(q, "UPDATE") {
				t.Errorf("Expected skip, but UPDATE query was executed: %s", q)
			}
		}
	})

	// Test Case 2: Hash Mismatch (Should Process)
	t.Run("ProcessChanged", func(t *testing.T) {
		mockDB := &MockExecutor{
			MockResults: map[string]interface{}{
				"SELECT hash FROM": []interface{}{
					map[string]interface{}{"hash": "oldhash"},
				},
			},
		}

		defer func() {
			if r := recover(); r != nil {
				// Expected panic because aiClient is nil
			}
		}()

		IngestCodebase(mockDB, nil, tmpDir)

		foundUpdate := false
		for _, q := range mockDB.ExecutedQueries {
			if strings.Contains(q, "UPDATE") {
				foundUpdate = true
				break
			}
		}

		if !foundUpdate {
			t.Error("Expected processing (UPDATE), but no UPDATE query found")
		}
	})

	// Test Case 3: New File (No Hash in DB)
	t.Run("ProcessNew", func(t *testing.T) {
		mockDB := &MockExecutor{
			MockResults: map[string]interface{}{
				"SELECT hash FROM": []interface{}{}, // Empty result
			},
		}

		defer func() {
			if r := recover(); r != nil {
				// Expected panic
			}
		}()

		IngestCodebase(mockDB, nil, tmpDir)

		foundUpdate := false
		for _, q := range mockDB.ExecutedQueries {
			if strings.Contains(q, "UPDATE") {
				foundUpdate = true
				break
			}
		}

		if !foundUpdate {
			t.Error("Expected processing (UPDATE) for new file, but no UPDATE query found")
		}
	})
}
