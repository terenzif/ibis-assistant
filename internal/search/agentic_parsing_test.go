package search

import (
	"context"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/ai"
)

// TestAskProjectAgentic_Parsing verifies that SEARCH commands without quotes
// but with trailing chatter are correctly parsed.
func TestAskProjectAgentic_Parsing(t *testing.T) {
	tests := []struct {
		name           string
		modelResponse  string
		expectedSearch string
	}{
		{
			name:           "No quotes with trailing sentence",
			modelResponse:  `SEARCH: foobar. I need it.`,
			expectedSearch: "foobar",
		},
		{
			name:           "No quotes with filename",
			modelResponse:  `SEARCH: user.go`,
			expectedSearch: "user.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB := &TestAgentMockDB{
				ReturnData: map[string]interface{}{},
			}

			stepCounter := 0
			mockAI := &TestAgentMockAI{
				GenerateFunc: func(contents []ai.Content) (ai.Candidate, error) {
					stepCounter++
					if stepCounter == 1 {
						return ai.Candidate{
							Content: ai.Content{
								Role: "model",
								Parts: []ai.Part{{Text: tt.modelResponse}},
							},
						}, nil
					}
					return ai.Candidate{
						Content: ai.Content{
							Role: "model",
							Parts: []ai.Part{{Text: "FINAL ANSWER: done"}},
						},
					}, nil
				},
			}

			svc := &Service{
				DB: mockDB,
				AI: mockAI,
			}

			_, _ = svc.AskProjectAgentic(context.Background(), "query")

			if len(mockAI.EmbedCalls) == 0 {
				t.Errorf("Expected search to be triggered, but it wasn't")
			} else {
				capturedQuery := mockAI.EmbedCalls[len(mockAI.EmbedCalls)-1]
				if capturedQuery != tt.expectedSearch {
					t.Errorf("Expected search query '%s', got '%s'", tt.expectedSearch, capturedQuery)
				}
			}
		})
	}
}
