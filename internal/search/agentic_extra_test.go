package search

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/ai"
)

// TestAskProjectAgentic_QueryParsing checks edge cases for query extraction
func TestAskProjectAgentic_QueryParsing(t *testing.T) {
	tests := []struct {
		name           string
		modelResponse  string
		expectedSearch string
		expectSearch   bool
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
					if res.Answer != "" && strings.Contains(tt.modelResponse, "FINAL ANSWER") {
						t.Errorf("Search was skipped because FINAL ANSWER was prioritized.")
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

// TestAskProjectAgentic_EmptyQuery checks that empty or punctuation-only queries don't trigger search
func TestAskProjectAgentic_EmptyQuery(t *testing.T) {
	tests := []struct {
		name          string
		modelResponse string
	}{
		{
			name:          "Empty Quotes",
			modelResponse: `SEARCH: ""`,
		},
		{
			name:          "Just Dot",
			modelResponse: `SEARCH: .`,
		},
		{
			name:          "Empty Line",
			modelResponse: `SEARCH: `,
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

					// Check observation
					lastMsg := contents[len(contents)-1]
					// We expect observation about empty query
					if strings.Contains(lastMsg.Parts[0].Text, "OBSERVATION: Found the following files") {
						t.Errorf("Expected observation about empty query, got search results")
					}
					if strings.Contains(lastMsg.Parts[0].Text, "No results found") {
						// This is what currently happens if search runs with empty string?
						// Or maybe "Search failed".
						// We want to differentiate between "Search ran and found nothing" and "Search didn't run".
						// But for this test, we check mockAI.EmbedCalls below.
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

			_, err := svc.AskProjectAgentic(context.Background(), "query")
			if err != nil {
				t.Fatalf("AskProjectAgentic failed: %v", err)
			}

			if len(mockAI.EmbedCalls) > 0 {
				t.Errorf("Expected NO search calls for empty query, got: %v", mockAI.EmbedCalls)
			}
		})
	}
}

// FailingAIMock helps test error handling
type FailingAIMock struct{}

func (m *FailingAIMock) EmbedText(ctx context.Context, text string) ([]float32, error) {
	return nil, fmt.Errorf("simulated embedding failure")
}

func (m *FailingAIMock) GenerateContent(ctx context.Context, contents []ai.Content, config ai.GenerationConfig) (ai.Candidate, error) {
	// Simple state machine:
	// 1. Request Search
	// 2. Receive Error -> Answer

	if len(contents) == 1 { // Only user prompt
		return ai.Candidate{
			Content: ai.Content{
				Role: "model",
				Parts: []ai.Part{{Text: "SEARCH: fail_please"}},
			},
		}, nil
	}

	lastMsg := contents[len(contents)-1]
	return ai.Candidate{
		Content: ai.Content{
			Role: "model",
			Parts: []ai.Part{{Text: fmt.Sprintf("FINAL ANSWER: Observed: %s", lastMsg.Parts[0].Text)}},
		},
	}, nil
}

func TestAskProjectAgentic_SearchError(t *testing.T) {
	mockDB := &TestAgentMockDB{
		ReturnData: map[string]interface{}{},
	}
	mockAI := &FailingAIMock{}

	svc := &Service{
		DB: mockDB,
		AI: mockAI,
	}

	res, err := svc.AskProjectAgentic(context.Background(), "query")
	if err != nil {
		t.Fatalf("AskProjectAgentic failed: %v", err)
	}

	if !strings.Contains(res.Answer, "Observed: OBSERVATION: Search failed") {
		t.Errorf("Expected answer to contain failure observation, got: %s", res.Answer)
	}
}
