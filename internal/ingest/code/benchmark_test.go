package code

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// MockEmbedder simulates AI client
type MockEmbedder struct {
	BatchLatency time.Duration
}

func (m *MockEmbedder) EmbedText(text string) ([]float32, error) {
	return make([]float32, 768), nil
}

func (m *MockEmbedder) BatchEmbedText(texts []string) ([][]float32, error) {
	if m.BatchLatency > 0 {
		time.Sleep(m.BatchLatency)
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, 768)
	}
	return out, nil
}

// MockExecutor simulates DB client
type MockExecutor struct {
	ExecCount   int
	QueryLatency time.Duration
}

func (m *MockExecutor) Execute(sql string) (interface{}, error) {
	m.ExecCount++
	if m.QueryLatency > 0 {
		time.Sleep(m.QueryLatency)
	}
	// Mimic a "SELECT hash" returning empty or something if needed,
	// but IngestCodebase doesn't check return value deeply in current impl.
	return nil, nil
}

func (m *MockExecutor) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	m.ExecCount++
	return nil, nil
}

func (m *MockExecutor) Close() {}

func setupBenchmarkRepo(b *testing.B, fileCount int) string {
	dir := b.TempDir()
	// Create content larger than chunk size (1000)
	// 100 chars * 100 lines = 10,000 chars -> 10 chunks
	baseLine := "This is a line of code that is reasonably long to simulate content in a source file for embedding.\n"
	var contentBuilder string
	for k := 0; k < 100; k++ {
		contentBuilder += baseLine
	}
	content := []byte(contentBuilder)

	for i := 0; i < fileCount; i++ {
		name := fmt.Sprintf("file_%d.go", i)
		err := os.WriteFile(filepath.Join(dir, name), content, 0644)
		if err != nil {
			b.Fatal(err)
		}
	}
	return dir
}

func BenchmarkIngestCodebase(b *testing.B) {
	// Setup
	// 100 files
	repoPath := setupBenchmarkRepo(b, 100)

	db := &MockExecutor{QueryLatency: 1 * time.Millisecond} // 1ms per DB call
	ai := &MockEmbedder{BatchLatency: 10 * time.Millisecond} // 10ms per batch

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// We re-run ingest on same files.
		// Current logic hashes files, checks DB (mock returns nil so it thinks empty?), then updates.
		// Since Mock DB doesn't store state, it will always process.
		err := IngestCodebase(db, ai, repoPath)
		if err != nil {
			b.Fatalf("Ingest failed: %v", err)
		}
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/schema"
)

type MockDB struct {
	ExecuteCount int
	MockResult   interface{} // To return on SELECT
}

func (m *MockDB) Execute(sql string) (interface{}, error) {
	m.ExecuteCount++
	if strings.HasPrefix(strings.TrimSpace(sql), "SELECT") && m.MockResult != nil {
		return m.MockResult, nil
	}
	return nil, nil
}

func (m *MockDB) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	m.ExecuteCount++
	return nil, nil
}

func (m *MockDB) Close() {
	// No-op
}

type MockAI struct {
	CallCount int
}

func (m *MockAI) BatchEmbedText(texts []string) ([][]float32, error) {
	m.CallCount++
	// Return dummy vectors
	vectors := make([][]float32, len(texts))
	for i := range texts {
		vectors[i] = []float32{0.1, 0.2, 0.3}
	}
	return vectors, nil
}

func TestIngestPerformance(t *testing.T) {
	// 1. Create Temp Dir with files
	tempDir, err := os.MkdirTemp("", "ingest_bench")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	fileCount := 50
	for i := 0; i < fileCount; i++ {
		name := filepath.Join(tempDir, fmt.Sprintf("file_%d.go", i))
		content := fmt.Sprintf("package main\n\nfunc main() {\n  println(\"Hello %d\")\n}", i)
		if err := os.WriteFile(name, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// 2. Run Ingest
	mockDB := &MockDB{}
	mockAI := &MockAI{}

	// Scenario 1: Clean start (DB returns nothing)
	err = IngestCodebase(mockDB, mockAI, tempDir)
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	t.Logf("Scenario 1 (Fresh): DB Calls: %d, AI Calls: %d", mockDB.ExecuteCount, mockAI.CallCount)

	if mockDB.ExecuteCount > 5 {
		t.Errorf("Fresh Ingest Regression: Expected <= 5 calls, got %d", mockDB.ExecuteCount)
	}
	if mockAI.CallCount == 0 {
		t.Errorf("Fresh Ingest: Expected AI calls, got 0")
	}

	// Scenario 2: Incremental (DB returns hashes, files unchanged)
	// Reset counters
	mockDB.ExecuteCount = 0
	mockAI.CallCount = 0

	// Mock DB returning valid hashes for all files
	var mockData []interface{}
	for i := 0; i < fileCount; i++ {
		path := filepath.Join(tempDir, fmt.Sprintf("file_%d.go", i))
		h, err := fileHash(path)
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("%s:%s", schema.TableFile, sanitizeID(path))
		mockData = append(mockData, map[string]interface{}{
			"id":   id,
			"hash": h,
		})
	}
	mockDB.MockResult = mockData

	err = IngestCodebase(mockDB, mockAI, tempDir)
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	t.Logf("Scenario 2 (Incremental): DB Calls: %d, AI Calls: %d", mockDB.ExecuteCount, mockAI.CallCount)

	// Expect 1 batch SELECT call. No writes.
	if mockDB.ExecuteCount > 1 {
		t.Errorf("Incremental Ingest: Expected 1 DB call (Select), got %d", mockDB.ExecuteCount)
	}
	if mockAI.CallCount > 0 {
		t.Errorf("Incremental Ingest: Expected 0 AI calls (All Skipped), got %d", mockAI.CallCount)
	}
}
