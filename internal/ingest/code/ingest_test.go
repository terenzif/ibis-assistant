package code

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/config"
	"github.com/deckonline/knowledge_mcp/internal/db"
)

// MockDB implements db.Executor
type MockDB struct {
	ExecuteCalls []string
	ReturnData   map[string]interface{} // Map SQL prefix -> Return value
}

func (m *MockDB) Execute(sql string) (interface{}, error) {
	m.ExecuteCalls = append(m.ExecuteCalls, sql)
	for prefix, val := range m.ReturnData {
		if strings.HasPrefix(sql, prefix) {
			return val, nil
		}
	}
	return nil, nil
}

func (m *MockDB) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	return nil, nil
}

func (m *MockDB) Close() {}

// MockAI implements AIClient
type MockAI struct {
	// No methods needed for async ingestion testing
}

func (m *MockAI) IsFunctional() bool {
	return true
}

func getFileHashHelper(path string) string {
	f, _ := os.Open(path)
	defer f.Close()
	h, _ := fileHash(f)
	return h
}

func BenchmarkIngestCodebase_NoChange(b *testing.B) {
	// Setup temp repo
	tmpDir, err := os.MkdirTemp("", "repo_bench")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "main.go")
	content := "package main\nfunc main() {}" + strings.Repeat("// comment\n", 100) // decently sized
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		b.Fatal(err)
	}

	expectedHash := getFileHashHelper(filePath)
	fileID := fmt.Sprintf("file:%s", db.SanitizeID(filePath))

	// We want to benchmark the loop where DB says "Hash Matches".
	// In the unoptimized version, this will still chunk and embed.
	// In the optimized version, it will just hash and query.

	mockDB := &MockDB{
		ReturnData: map[string]interface{}{
			fmt.Sprintf("SELECT hash FROM %s", fileID): []interface{}{
				map[string]interface{}{"hash": expectedHash},
			},
		},
	}
	mockAI := &MockAI{}
	cfg := config.Load()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset calls to avoid infinite growth if that matters (slices)
		mockDB.ExecuteCalls = nil

		err := IngestCodebase(context.Background(), mockDB, mockAI, tmpDir, cfg)
		if err != nil {
			b.Fatalf("Error: %v", err)
		}
	}
}

func TestIngestCodebase_Delta(t *testing.T) {
	// Setup temp repo
	tmpDir, err := os.MkdirTemp("", "repo_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "main.go")
	content := "package main\nfunc main() {}"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	expectedHash := getFileHashHelper(filePath)
	fileID := fmt.Sprintf("file:%s", db.SanitizeID(filePath))
	cfg := config.Load()

	t.Run("Skip Unchanged", func(t *testing.T) {
		mockDB := &MockDB{
			ReturnData: map[string]interface{}{
				fmt.Sprintf("SELECT hash FROM %s", fileID): []interface{}{
					map[string]interface{}{"hash": expectedHash},
				},
			},
		}
		mockAI := &MockAI{}

		err := IngestCodebase(context.Background(), mockDB, mockAI, tmpDir, cfg)
		if err != nil {
			t.Fatalf("IngestCodebase failed: %v", err)
		}

		// Verify no INSERT or CREATE calls
		for _, sql := range mockDB.ExecuteCalls {
			if strings.Contains(sql, "INSERT INTO file_chunk") || strings.Contains(sql, "CREATE file_chunk") {
				t.Errorf("Processed embedding (Unoptimized) - Expected 0 chunk updates, got call: %s", sql)
			}
		}
	})

	t.Run("Update Chunks", func(t *testing.T) {
		// Force update by not returning hash match
		mockDB := &MockDB{
			ReturnData: map[string]interface{}{},
		}
		mockAI := &MockAI{}

		err := IngestCodebase(context.Background(), mockDB, mockAI, tmpDir, cfg)
		if err != nil {
			t.Fatalf("IngestCodebase failed: %v", err)
		}

		// Verify CREATE is called for chunks
		foundUpdate := false
		for _, sql := range mockDB.ExecuteCalls {
			// Check for assignment in CREATE
			if strings.Contains(sql, "CREATE file_chunk") && strings.Contains(sql, "batch_status='pending'") {
				foundUpdate = true
				break
			}
		}
		if !foundUpdate {
			t.Errorf("Expected CREATE file_chunk statement with batch_status='pending', but not found. Calls: %v", mockDB.ExecuteCalls)
		}
	})
}

func TestIngestCodebase_Exclusions(t *testing.T) {
	// Setup temp repo
	tmpDir, err := os.MkdirTemp("", "repo_exclusions")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create structure:
	// /main.go          (Supported)
	// /ignored_dir/     (Ignored Directory)
	//   /ignored.go
	// /package-lock.json(Ignored File)
	// /large.go         (Too large)
	// /other.txt        (Unsupported extension)

	// 1. Supported File
	mainGo := filepath.Join(tmpDir, "main.go")
	os.WriteFile(mainGo, []byte("package main"), 0644)

	// 2. Ignored Directory
	ignoredDir := filepath.Join(tmpDir, "node_modules")
	os.Mkdir(ignoredDir, 0755)
	os.WriteFile(filepath.Join(ignoredDir, "lib.js"), []byte("console.log()"), 0644)

	// 3. Ignored File
	lockFile := filepath.Join(tmpDir, "package-lock.json")
	os.WriteFile(lockFile, []byte("{}"), 0644)

	// 4. Large File (limit is small for test)
	largeFile := filepath.Join(tmpDir, "large.go")
	os.WriteFile(largeFile, []byte(strings.Repeat("a", 200)), 0644)

	// 5. Unsupported Extension
	txtFile := filepath.Join(tmpDir, "readme.txt")
	os.WriteFile(txtFile, []byte("readme"), 0644)

	// Setup Config
	cfg := &config.Config{
		MaxFileSize:         100, // 100 bytes limit
		IgnoredDirs:         []string{"node_modules"},
		IgnoredFiles:        []string{"package-lock.json"},
		SupportedExtensions: []string{".go", ".js"},
	}

	mockDB := &MockDB{ReturnData: map[string]interface{}{}}
	mockAI := &MockAI{}

	err = IngestCodebase(context.Background(), mockDB, mockAI, tmpDir, cfg)
	if err != nil {
		t.Fatalf("IngestCodebase failed: %v", err)
	}

	// Analyze Calls
	// We expect processFile to be called only for main.go
	// Helper to check if a path was processed
	wasProcessed := func(path string) bool {
		sanitized := db.SanitizeID(path)
		target := fmt.Sprintf("file:%s", sanitized)
		for _, sql := range mockDB.ExecuteCalls {
			if strings.Contains(sql, target) {
				return true
			}
		}
		return false
	}

	if !wasProcessed(mainGo) {
		t.Errorf("main.go should have been processed")
	}

	if wasProcessed(filepath.Join(ignoredDir, "lib.js")) {
		t.Errorf("node_modules/lib.js should have been ignored (Ignored Dir)")
	}

	if wasProcessed(lockFile) {
		t.Errorf("package-lock.json should have been ignored (Ignored File)")
	}

	if wasProcessed(largeFile) {
		t.Errorf("large.go should have been ignored (Too Large)")
	}

	if wasProcessed(txtFile) {
		t.Errorf("readme.txt should have been ignored (Unsupported Ext)")
	}
}

func TestIngestCodebase_Pruning(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Create a file that is ignored by .aiignore
	ignoredFile := filepath.Join(tmpDir, "secret.go")
	os.WriteFile(ignoredFile, []byte("secret"), 0644)

	// 2. Create .aiignore
	os.WriteFile(filepath.Join(tmpDir, ".aiignore"), []byte("secret.go"), 0644)

	// 3. Setup Config
	cfg := config.Load()

	// 4. Setup MockDB with existing files
	ignoredID := fmt.Sprintf("file:%s", db.SanitizeID(ignoredFile))

	// deletedFile (not on disk)
	deletedFile := filepath.Join(tmpDir, "gone.go")
	deletedID := fmt.Sprintf("file:%s", db.SanitizeID(deletedFile))

	// existingFile (should be kept)
	existingFile := filepath.Join(tmpDir, "main.go")
	os.WriteFile(existingFile, []byte("package main"), 0644)
	existingID := fmt.Sprintf("file:%s", db.SanitizeID(existingFile))

	// Mock Response
	mockDB := &MockDB{
		ReturnData: map[string]interface{}{
			"SELECT id, path FROM file": []interface{}{
				map[string]interface{}{"id": ignoredID, "path": ignoredFile},
				map[string]interface{}{"id": deletedID, "path": deletedFile},
				map[string]interface{}{"id": existingID, "path": existingFile},
			},
		},
	}
	mockAI := &MockAI{}

	err := IngestCodebase(context.Background(), mockDB, mockAI, tmpDir, cfg)
	if err != nil {
		t.Fatalf("Error: %v", err)
	}

	// Verify Deletes
	deletedIDs := make(map[string]bool)
	for _, sql := range mockDB.ExecuteCalls {
		if strings.HasPrefix(sql, "DELETE") && !strings.Contains(sql, "file_chunk") {
			// This is a file record delete (PruneRepo)
			if strings.Contains(sql, ignoredID) {
				deletedIDs[ignoredID] = true
			}
			if strings.Contains(sql, deletedID) {
				deletedIDs[deletedID] = true
			}
			if strings.Contains(sql, existingID) {
				t.Errorf("Existing file %s was deleted! SQL: %s", existingID, sql)
			}
		}
	}

	if !deletedIDs[ignoredID] {
		t.Errorf("Ignored file was not deleted. Calls: %v", mockDB.ExecuteCalls)
	}
	if deletedIDs[deletedID] {
		t.Errorf("Deleted file (physically missing) WAS deleted from DB! It should be preserved.")
	}
}

func TestIngestCodebase_Versioning(t *testing.T) {
	// Scenario: File changes content V1 -> V2 -> V1
	// We want to ensure V1 embeddings are not regenerated when we switch back.

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "main.go")
	fileID := fmt.Sprintf("file:%s", db.SanitizeID(path))

	// Helper to set content and run ingest
	runIngest := func(content string, mockAI *MockAI, mockDB *MockDB) {
		os.WriteFile(path, []byte(content), 0644)
		IngestCodebase(context.Background(), mockDB, mockAI, tmpDir, config.Load())
	}

	// 1. Ingest V1
	mockDB := &MockDB{ReturnData: map[string]interface{}{}}
	mockAI := &MockAI{}
	runIngest("version1", mockAI, mockDB)

	// Check for CREATE (Queueing)
	foundInsert := false
	for _, sql := range mockDB.ExecuteCalls {
		if strings.Contains(sql, "CREATE file_chunk") && strings.Contains(sql, "batch_status='pending'") {
			foundInsert = true
			break
		}
	}
	if !foundInsert {
		t.Errorf("Expected V1 to be queued (CREATE file_chunk ... pending)")
	}

	// 2. Ingest V2 (Different content)
	mockDB = &MockDB{ReturnData: map[string]interface{}{}} // Reset
	mockAI = &MockAI{}
	runIngest("version2", mockAI, mockDB)

	// Check for CREATE (Queueing)
	foundInsertV2 := false
	for _, sql := range mockDB.ExecuteCalls {
		if strings.Contains(sql, "CREATE file_chunk") && strings.Contains(sql, "batch_status='pending'") {
			foundInsertV2 = true
			break
		}
	}
	if !foundInsertV2 {
		t.Errorf("Expected V2 to be queued")
	}

	// 3. Ingest V1 again (Switch back)
	// DB has V2 hash.
	// Ingest calculates V1 hash.
	// Queries "SELECT count() ... WHERE hash=V1_HASH".
	// WE NEED TO MOCK THIS RETURN to > 0

	v1Hash, _ := getHash("version1")
	countQuery := fmt.Sprintf("SELECT count() FROM file_chunk WHERE file = %s AND hash = '%s';", fileID, v1Hash)

	mockDB = &MockDB{
		ReturnData: map[string]interface{}{
			countQuery: []interface{}{
				map[string]interface{}{"count": 5}, // Assume 5 chunks exist
			},
		},
	}
	mockAI = &MockAI{}
	runIngest("version1", mockAI, mockDB)

	// Verify NO queuing happened
	for _, sql := range mockDB.ExecuteCalls {
		if (strings.Contains(sql, "INSERT INTO file_chunk") || strings.Contains(sql, "CREATE file_chunk")) && strings.Contains(sql, "batch_status='pending'") {
			t.Errorf("Expected 0 queuing calls for returning to V1 (Cache Hit), got call: %s", sql)
		}
	}
}

func getHash(s string) (string, error) {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil)), nil
}

func BenchmarkIngestCodebase_Write(b *testing.B) {
	// Setup temp repo
	tmpDir, err := os.MkdirTemp("", "repo_write_bench")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a large file to generate many chunks
	filePath := filepath.Join(tmpDir, "large.go")
	line := strings.Repeat("A", 100) + "\n"
	content := strings.Repeat(line, 2000) // 200KB

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		b.Fatal(err)
	}

	mockDB := &MockDB{
		ReturnData: map[string]interface{}{}, // Always return nil -> force write
	}
	mockAI := &MockAI{}
	cfg := config.Load()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mockDB.ExecuteCalls = nil

		err := IngestCodebase(context.Background(), mockDB, mockAI, tmpDir, cfg)
		if err != nil {
			b.Fatalf("Error: %v", err)
		}
	}
}

func TestIngestCodebase_Batching(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file large enough to produce > 10 chunks (batch size)
	// 25 chunks should result in 1 batch (batch size 100).
	filePath := filepath.Join(tmpDir, "batch.go")
	line := strings.Repeat("A", 100) + "\n"
	content := strings.Repeat(line, 250) // ~25KB. Chunk size 1000 => ~25 chunks.

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	mockDB := &MockDB{
		ReturnData: map[string]interface{}{}, // Force updates
	}
	mockAI := &MockAI{}
	cfg := config.Load()

	err := IngestCodebase(context.Background(), mockDB, mockAI, tmpDir, cfg)
	if err != nil {
		t.Fatalf("Error: %v", err)
	}

	// Count DB update calls
	insertCount := 0
	for _, sql := range mockDB.ExecuteCalls {
		if strings.Contains(sql, "BEGIN TRANSACTION") {
			insertCount++
		}
	}

	// We expect 1 batch (25 chunks < 100)
	if insertCount != 1 {
		t.Errorf("Expected 1 batched INSERT, got %d", insertCount)
	}
}

func TestIngestCodebase_Pruning_QuerySyntax(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Load()

	// Mock DB to capture query
	mockDB := &MockDB{
		ReturnData: map[string]interface{}{},
	}
	mockAI := &MockAI{}

	// We just need to trigger pruneRepo.
	// It's called at the end of IngestCodebase.
	err := IngestCodebase(context.Background(), mockDB, mockAI, tmpDir, cfg)
	if err != nil {
		t.Fatalf("IngestCodebase failed: %v", err)
	}

	foundQuery := false
	for _, sql := range mockDB.ExecuteCalls {
		if strings.Contains(sql, "SELECT id, path FROM file WHERE") {
			foundQuery = true
			if strings.Contains(sql, "BEGINSWITH") {
				t.Errorf("Query uses deprecated BEGINSWITH syntax: %s", sql)
			}
			if !strings.Contains(sql, "string::starts_with") {
				t.Errorf("Query should use string::starts_with syntax: %s", sql)
			}
		}
	}

	if !foundQuery {
		t.Errorf("Pruning query not found in calls: %v", mockDB.ExecuteCalls)
	}
}
