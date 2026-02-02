package code

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	fileID := fmt.Sprintf("file:%s", sanitizeID(filePath))

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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset calls to avoid infinite growth if that matters (slices)
		mockDB.ExecuteCalls = nil
		mockAI.BatchEmbedCalls = nil

		err := IngestCodebase(mockDB, mockAI, tmpDir)
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
	fileID := fmt.Sprintf("file:%s", sanitizeID(filePath))

	t.Run("Skip Unchanged", func(t *testing.T) {
		mockDB := &MockDB{
			ReturnData: map[string]interface{}{
				fmt.Sprintf("SELECT hash FROM %s", fileID): []interface{}{
					map[string]interface{}{"hash": expectedHash},
				},
			},
		}
		mockAI := &MockAI{}

		err := IngestCodebase(mockDB, mockAI, tmpDir)
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
}
