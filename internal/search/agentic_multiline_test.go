package search

import (
	"context"
	"testing"

	"github.com/terenzif/ibis-assistant/internal/ai"
)

func TestAskProjectAgentic_MultilineQuery(t *testing.T) {
	mockDB := &TestAgentMockDB{
		ReturnData: map[string]interface{}{},
	}

	// Case: Model outputs a multiline quoted query
	multilineQuery := "SEARCH: \"auth logic\nand user session\""
	expectedQuery := "auth logic\nand user session"

	stepCounter := 0
	mockAI := &TestAgentMockAI{
		GenerateFunc: func(contents []ai.Content) (ai.Candidate, error) {
			stepCounter++
			if stepCounter == 1 {
				return ai.Candidate{
					Content: ai.Content{
						Role:  "model",
						Parts: []ai.Part{{Text: multilineQuery}},
					},
				}, nil
			}
			return ai.Candidate{
				Content: ai.Content{
					Role:  "model",
					Parts: []ai.Part{{Text: "FINAL ANSWER: done"}},
				},
			}, nil
		},
	}

	svc := &Service{
		DB: mockDB,
		AI: mockAI,
	}

	_, err := svc.AskProjectAgentic(context.Background(), "query", "")
	if err != nil {
		t.Fatalf("AskProjectAgentic failed: %v", err)
	}

	if len(mockAI.EmbedCalls) == 0 {
		t.Errorf("Expected search to be triggered, but it wasn't")
	} else {
		lastCall := mockAI.EmbedCalls[len(mockAI.EmbedCalls)-1]
		// The current implementation likely truncates at newline, so we expect failure here if we assert full query
		if lastCall != expectedQuery {
			t.Errorf("Expected search query '%s', got '%s'", expectedQuery, lastCall)
		}
	}
}
