package search

import (
	"context"
	"testing"

	"github.com/terenzif/ibis-server/internal/ai"
)

func TestAskProjectAgentic_Robustness(t *testing.T) {
	tests := []struct {
		name           string
		modelResponse  string
		expectedSearch string
		expectSearch   bool
	}{
		{
			name:           "Standard Format",
			modelResponse:  "THOUGHT: Need to search.\nSEARCH: auth logic",
			expectedSearch: "auth logic",
			expectSearch:   true,
		},
		{
			name:           "Quoted Query",
			modelResponse:  "THOUGHT: Searching exact phrase.\nSEARCH: \"auth logic\"",
			expectedSearch: "auth logic", // Expect quotes to be stripped
			expectSearch:   true,
		},
		{
			name:           "Case Sensitivity (Search:)",
			modelResponse:  "THOUGHT: I will search.\nSearch: auth logic",
			expectedSearch: "auth logic",
			expectSearch:   true,
		},
		{
			name:           "Trailing Text",
			modelResponse:  "SEARCH: auth logic   \nNext line",
			expectedSearch: "auth logic",
			expectSearch:   true,
		},
		{
			name:           "Mid-sentence",
			modelResponse:  "I will run SEARCH: auth logic to find out.",
			expectedSearch: "auth logic to find out",
			expectSearch:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB := &TestAgentMockDB{
				ReturnData: map[string]interface{}{},
			}

			// Mock AI to return the specific response then a Final Answer to stop the loop
			stepCounter := 0
			mockAI := &TestAgentMockAI{
				GenerateFunc: func(contents []ai.Content) (ai.Candidate, error) {
					stepCounter++
					if stepCounter == 1 {
						return ai.Candidate{
							Content: ai.Content{
								Role:  "model",
								Parts: []ai.Part{{Text: tt.modelResponse}},
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

			// Verify if search was triggered and check query content
			if tt.expectSearch {
				if len(mockAI.EmbedCalls) == 0 {
					t.Errorf("Expected search to be triggered (EmbedText called), but it wasn't")
				} else {
					lastCall := mockAI.EmbedCalls[len(mockAI.EmbedCalls)-1]
					if lastCall != tt.expectedSearch {
						t.Errorf("Expected search query '%s', got '%s'", tt.expectedSearch, lastCall)
					}
				}
			} else {
				if len(mockAI.EmbedCalls) > 0 {
					t.Errorf("Expected NO search, but EmbedText was called: %v", mockAI.EmbedCalls)
				}
			}
		})
	}
}
