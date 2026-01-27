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
	}
}
