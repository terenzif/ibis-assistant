package search

import (
	"context"
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/ai"
)

// TestAgentMockDB and TestAgentMockAI are defined in agentic_robustness_test.go
// We need to redefine or import them. Since they are in the same package, we can reuse them if they are exported.
// Wait, agentic_robustness_test.go defines them but unexported? No, checking the file content again.
// The file content showed: `mockDB := &TestAgentMockDB{...}`
// This suggests TestAgentMockDB is defined in the package scope, likely in `agentic_test.go` or `agentic_robustness_test.go`.

// Let's verify where `TestAgentMockDB` is defined.
// If it's in `agentic_test.go`, it's available here.

func TestAskProjectAgentic_ExtraRobustness(t *testing.T) {
	tests := []struct {
		name           string
		modelResponse  string
		expectedSearch string
		expectSearch   bool
		expectedAnswer string
	}{
		{
			name:           "Quotes with trailing text",
			modelResponse:  `SEARCH: "foobar" because I need it.`,
			expectedSearch: "foobar",
			expectSearch:   true,
		},
		{
			name:           "Quotes with trailing punctuation",
			modelResponse:  `SEARCH: "foobar".`,
			expectedSearch: "foobar",
			expectSearch:   true,
		},
		{
			name:           "No quotes with trailing punctuation",
			modelResponse:  `SEARCH: foobar.`,
			expectedSearch: "foobar",
			expectSearch:   true,
		},
		{
			name:           "Both Search and Final Answer (Search First)",
			modelResponse:  `SEARCH: "foobar" then FINAL ANSWER: done`,
			expectedSearch: "foobar",
			expectSearch:   true,
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

			res, err := svc.AskProjectAgentic(context.Background(), "query")
			if err != nil {
				t.Fatalf("AskProjectAgentic failed: %v", err)
			}

			if tt.expectSearch {
				if len(mockAI.EmbedCalls) == 0 {
					// Check if it returned an answer instead
					if res.Answer != "" && strings.Contains(tt.modelResponse, "FINAL ANSWER") {
						t.Logf("Search was skipped because FINAL ANSWER was prioritized. Answer: %s", res.Answer)
						// This is the behavior we want to test/change?
						// For now, let's fail if we EXPECT search but got none.
						t.Errorf("Expected search to be triggered, but it wasn't")
					} else {
						t.Errorf("Expected search to be triggered, but it wasn't")
					}
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
