package code

import (
	"context"
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
	BatchEmbedCalls [][]string
}

func (m *MockAI) BatchEmbedText(texts []string) ([][]float32, error) {
	m.BatchEmbedCalls = append(m.BatchEmbedCalls, texts)
	// Return dummy embeddings
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = []float32{0.1, 0.2, 0.3}
	}
	return result, nil
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
		mockAI.BatchEmbedCalls = nil

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

		// After optimization, this should be 0.
		if len(mockAI.BatchEmbedCalls) == 0 {
			t.Log("Skipped embedding (Optimized)")
		} else {
			t.Errorf("Processed embedding (Unoptimized) - Expected 0 calls, got %d", len(mockAI.BatchEmbedCalls))
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

		// Verify UPDATE is called for chunks
		foundUpdate := false
		for _, sql := range mockDB.ExecuteCalls {
			if strings.HasPrefix(sql, "UPDATE file_chunk") {
				foundUpdate = true
				break
			}
		}
		if !foundUpdate {
			t.Errorf("Expected UPDATE file_chunk statement, but not found. Calls: %v", mockDB.ExecuteCalls)
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

	// 1. Create a file that is ignored by .knowledgeignore
	ignoredFile := filepath.Join(tmpDir, "secret.go")
	os.WriteFile(ignoredFile, []byte("secret"), 0644)

	// 2. Create .knowledgeignore
	os.WriteFile(filepath.Join(tmpDir, ".knowledgeignore"), []byte("secret.go"), 0644)

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
	if !deletedIDs[deletedID] {
		t.Errorf("Deleted file was not deleted. Calls: %v", mockDB.ExecuteCalls)
	}
}
