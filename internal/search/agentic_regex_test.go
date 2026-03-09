package search

import (
	"context"
	"strings"
	"testing"
	"github.com/deckonline/knowledge_mcp/internal/ai"
)

func TestAskProjectAgentic_RegexOptimization(t *testing.T) {
	// This test verifies that the agentic loop correctly identifies SEARCH vs FINAL ANSWER
	// even after we move regex compilation out of the loop.

	mockDB := &TestAgentMockDB{
		ReturnData: map[string]interface{}{
			"FROM file_chunk": []map[string]interface{}{},
		},
	}

	step := 0
	mockAI := &TestAgentMockAI{
		GenerateFunc: func(contents []ai.Content) (ai.Candidate, error) {
			step++
			if step == 1 {
				// Case 1: SEARCH comes first
				return ai.Candidate{
					Content: ai.Content{
						Role: "model",
						Parts: []ai.Part{
							{Text: "THOUGHT: Checking code.\nSEARCH: \"something\""},
						},
					},
				}, nil
			} else if step == 2 {
				// Case 2: FINAL ANSWER
				return ai.Candidate{
					Content: ai.Content{
						Role: "model",
						Parts: []ai.Part{
							{Text: "FINAL ANSWER: Done."},
						},
					},
				}, nil
			}
			return ai.Candidate{}, nil
		},
	}

	svc := &Service{
		DB: mockDB,
		AI: mockAI,
	}

	result, err := svc.AskProjectAgentic(context.Background(), "test")
	if err != nil {
		t.Fatalf("AskProjectAgentic failed: %v", err)
	}

	if result.Answer != "Done." {
		t.Errorf("Expected answer 'Done.', got '%s'", result.Answer)
	}

	// Verify that search was executed (mockDB captured queries)
	foundSearch := false
	for _, sql := range mockDB.CapturedQueries {
		if strings.Contains(sql, "file_chunk") {
			foundSearch = true
			break
		}
	}
	if !foundSearch {
		t.Error("Expected search to be executed in step 1")
	}
}
