package search

import (
	"context"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/ai"
)

func TestAskProjectAgentic(t *testing.T) {
	// 1. Setup Mock DB for AskProject
	// We need AskProject to return something when SEARCH is called.
	mockDB := &MockDB{
		ReturnData: map[string]interface{}{
			"FROM file_chunk": []map[string]interface{}{
				{"id": "chunk:1", "path": "/src/main.go", "content": "func main() { logic }", "score": 0.9},
			},
		},
	}

	// 2. Setup Mock AI to simulate ReAct loop
	step := 0
	mockAI := &MockAI{
		GenerateFunc: func(history []ai.Content) (ai.Candidate, error) {
			step++
			// Step 1: Agent asks to SEARCH
			if step == 1 {
				return ai.Candidate{Content: ai.Content{Parts: []ai.Part{{Text: "THOUGHT: I need to check main.go\nSEARCH: main function"}}}}, nil
			}
			// Step 2: Agent analyzes results and gives FINAL ANSWER
			// History should contain the user prompt + Agent Step 1 + User Observation
			return ai.Candidate{Content: ai.Content{Parts: []ai.Part{{Text: "FINAL ANSWER: The main function has logic."}}}}, nil
		},
	}

	svc := &Service{
		DB: mockDB,
		AI: mockAI,
	}

	// 3. Run
	res, err := svc.AskProjectAgentic(context.Background(), "What is in main?")
	if err != nil {
		t.Fatalf("Agentic search failed: %v", err)
	}

	// 4. Verify
	if res.Answer != "The main function has logic." {
		t.Errorf("Expected answer 'The main function has logic.', got '%s'", res.Answer)
	}

	if len(res.Steps) != 2 {
		t.Errorf("Expected 2 steps, got %d", len(res.Steps))
	}

	// Check if source was captured
	if len(res.Sources) != 1 {
		t.Errorf("Expected 1 source, got %d", len(res.Sources))
	}
}
