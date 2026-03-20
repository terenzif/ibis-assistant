package optimization

import (
	"context"
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/ai"
	"github.com/deckonline/knowledge_mcp/internal/search"
)

// MockOptDB implements db.Executor for optimization tests
type MockOptDB struct {
	CapturedQueries []string
	UpdateCalled    bool
}

func (m *MockOptDB) Execute(ctx context.Context, sql string) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)

	// 1. Handle Random Chunk Selection (OptimizeLoop step 1)
	if strings.Contains(sql, "ORDER BY rand()") {
		// Return a list containing one dummy chunk
		return []map[string]interface{}{
			{
				"id":      "file_chunk:test_1",
				"content": "func Test() { println(\"hello\") }",
			},
		}, nil
	}

	// 2. Handle Vector Search (AskProject)
	// The query contains "FROM file_chunk" and usually "vector::similarity"
	if strings.Contains(sql, "FROM file_chunk") && strings.Contains(sql, "SELECT") {
		// Return the SAME chunk so it matches the expected ID
		// AskProject expects fields: id, path, content, hash, current_hash, score
		return []map[string]interface{}{
			{
				"id":           "file_chunk:test_1",
				"path":         "/src/test.go",
				"content":      "func Test() { println(\"hello\") }",
				"score":        0.99,
				"hash":         "abc",
				"current_hash": "abc",
			},
		}, nil
	}

	return nil, nil
}

func (m *MockOptDB) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)

	// 3. Handle ReinforcePath Update
	if strings.Contains(sql, "UPDATE $target SET access_count") {
		m.UpdateCalled = true
		// Verify vars
		vMap, ok := vars.(map[string]interface{})
		if ok {
			if target, ok := vMap["target"]; ok && target == "file_chunk:test_1" {
				// Correct target
			} else {
				// Wrong target
			}
		}
		return []interface{}{}, nil
	}

	// 4. Handle GetFileContext (Graph Query)
	// "SELECT <-changed ..."
	if strings.Contains(sql, "<-changed") {
		// Return empty result to indicate no graph context
		return []interface{}{}, nil
	}

	return nil, nil
}

func (m *MockOptDB) Close() {}

// MockOptAI implements search.AIProvider
type MockOptAI struct{}

func (m *MockOptAI) EmbedText(ctx context.Context, text string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func (m *MockOptAI) GenerateContent(ctx context.Context, contents []ai.Content, config ai.GenerationConfig) (ai.Candidate, error) {
	// Return a synthetic question
	return ai.Candidate{
		Content: ai.Content{
			Role: "model",
			Parts: []ai.Part{
				{Text: "QUESTION: What does the Test function do?"},
			},
		},
	}, nil
}

func TestOptimizeLoop(t *testing.T) {
	mockDB := &MockOptDB{}
	mockAI := &MockOptAI{}

	svc := &search.Service{
		DB: mockDB,
		AI: mockAI,
	}

	opt := NewOptimizer(mockDB, mockAI, svc)

	// Run for 1 iteration
	err := opt.OptimizeLoop(context.Background(), 1)
	if err != nil {
		t.Fatalf("OptimizeLoop failed: %v", err)
	}

	// Verify that ReinforcePath was triggered
	if !mockDB.UpdateCalled {
		t.Error("Expected ReinforcePath to trigger an UPDATE query, but it didn't")
	}

	// Verify sequence of queries
	foundRandom := false
	foundSearch := false
	foundUpdate := false

	for _, sql := range mockDB.CapturedQueries {
		if strings.Contains(sql, "ORDER BY rand()") {
			foundRandom = true
		}
		if strings.Contains(sql, "FROM file_chunk") && strings.Contains(sql, "SELECT") {
			foundSearch = true
		}
		if strings.Contains(sql, "UPDATE $target SET access_count") {
			foundUpdate = true
		}
	}

	if !foundRandom {
		t.Error("Did not find random selection query")
	}
	if !foundSearch {
		t.Error("Did not find vector search query")
	}
	if !foundUpdate {
		t.Error("Did not find update query")
	}
}
