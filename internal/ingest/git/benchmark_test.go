package git

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

type BenchMockDB struct {
	AllCommits []interface{}
}

func (m *BenchMockDB) Execute(sql string) (interface{}, error) {
	return nil, nil
}
func (m *BenchMockDB) Close() {}
func (m *BenchMockDB) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	// New Optimized Path: returns only requested IDs
	if strings.Contains(sql, "IN $ids") {
		// In the benchmark, the repo has a real commit hash (e.g. "a1b2...").
		// The Mock DB has "commit:hash_0"...
		// They won't match. Return empty.
		// Even if they matched, we'd return 1 item.
		// So returning empty list simulates "checked 1 item, found 0".
		return []interface{}{}, nil
	}

	// Old Path: returns all IDs for the repo
	return m.AllCommits, nil
}

// BenchmarkIngestStartup measures the memory and time cost of the "pre-load existing commits" phase.
// It mocks a DB with N existing commits and runs IngestRepo on an empty repo.
func BenchmarkIngestStartup(b *testing.B) {
	// 1. Setup minimal git repo so IngestRepo doesn't fail early
	repoDir := b.TempDir()
	exec.Command("git", "init", repoDir).Run()
	setupCmds := [][]string{
		{"git", "config", "user.name", "Bench"},
		{"git", "config", "user.email", "bench@example.com"},
		{"git", "commit", "--allow-empty", "-m", "init", "--allow-empty-message"},
	}
	for _, args := range setupCmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		cmd.Run()
	}

	// 2. Prepare large dataset of existing commits
	count := 100000 // 100k commits
	commits := make([]interface{}, count)
	for i := 0; i < count; i++ {
		commits[i] = map[string]interface{}{
			"id": fmt.Sprintf("commit:hash_%d", i),
		}
	}

	mockDB := &BenchMockDB{
		AllCommits: commits,
	}

	// Mock Redmine (not used but required)
	mockRedmine := &MockRedmine{}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		err := IngestRepo(mockDB, mockRedmine, repoDir, 10)
		if err != nil {
			b.Fatalf("IngestRepo failed: %v", err)
		}
	}
}

// BenchmarkParsing_Map simulates parsing a list of maps from []interface{}
func BenchmarkParsing_Map(b *testing.B) {
	// Setup large result set simulating DB return: []interface{} -> map[string]interface{} -> "id" -> string
	count := 1000
	results := make([]interface{}, count)
	for i := 0; i < count; i++ {
		results[i] = map[string]interface{}{
			"id": fmt.Sprintf("commit:hash_%d", i),
		}
	}

	// Simulate JSON unmarshal overhead if applicable, but we assume direct type assertion here
	// The current code does:
	/*
		if results, ok := resRaw.([]interface{}); ok {
			for _, item := range results {
				if props, ok := item.(map[string]interface{}); ok {
					if id, ok := props["id"].(string); ok {
						idMap[id] = true
					}
				}
			}
		}
	*/

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idMap := make(map[string]bool)
		for _, item := range results {
			if props, ok := item.(map[string]interface{}); ok {
				if id, ok := props["id"].(string); ok {
					idMap[id] = true
				}
			}
		}
	}
}

// BenchmarkParsing_String simulates parsing a list of strings from []interface{}
func BenchmarkParsing_String(b *testing.B) {
	// Setup result set simulating DB return: []interface{} -> string
	count := 1000
	results := make([]interface{}, count)
	for i := 0; i < count; i++ {
		results[i] = fmt.Sprintf("commit:hash_%d", i)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idMap := make(map[string]bool)
		for _, item := range results {
			if id, ok := item.(string); ok {
				idMap[id] = true
			}
		}
	}
}

// BenchmarkJSON_Map simulates JSON unmarshal overhead for Map based result
func BenchmarkJSON_Map(b *testing.B) {
	// Setup JSON payload
	count := 1000
	raw := make([]map[string]string, count)
	for i := 0; i < count; i++ {
		raw[i] = map[string]string{"id": fmt.Sprintf("commit:hash_%d", i)}
	}
	bytes, _ := json.Marshal(raw)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var res []map[string]interface{}
		json.Unmarshal(bytes, &res)
		idMap := make(map[string]bool)
		for _, item := range res {
			if id, ok := item["id"].(string); ok {
				idMap[id] = true
			}
		}
	}
}

// BenchmarkJSON_String simulates JSON unmarshal overhead for String based result
func BenchmarkJSON_String(b *testing.B) {
	// Setup JSON payload
	count := 1000
	raw := make([]string, count)
	for i := 0; i < count; i++ {
		raw[i] = fmt.Sprintf("commit:hash_%d", i)
	}
	bytes, _ := json.Marshal(raw)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var res []string
		json.Unmarshal(bytes, &res)
		idMap := make(map[string]bool)
		for _, id := range res {
			idMap[id] = true
		}
	}
}
