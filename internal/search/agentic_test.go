package search

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/deckonline/knowledge_mcp/internal/ai"
)

// TestAgentMockAI implements AIProvider for testing Agentic flow
type TestAgentMockAI struct {
	GenerateFunc func(contents []ai.Content) (ai.Candidate, error)
}

func (m *TestAgentMockAI) EmbedText(ctx context.Context, text string) ([]float32, error) {
	// Return a dummy vector
	return []float32{0.1, 0.2, 0.3}, nil
}

func (m *TestAgentMockAI) GenerateContent(ctx context.Context, contents []ai.Content, config ai.GenerationConfig) (ai.Candidate, error) {
	if m.GenerateFunc != nil {
		return m.GenerateFunc(contents)
	}
	return ai.Candidate{}, fmt.Errorf("GenerateFunc not implemented")
}

// TestAgentMockDB implements db.Executor for testing Agentic flow
type TestAgentMockDB struct {
	CapturedQueries []string
	ReturnData      map[string]interface{}
}

func (m *TestAgentMockDB) Execute(sql string) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	for k, v := range m.ReturnData {
		if strings.Contains(sql, k) {
			return v, nil
		}
	}
	return nil, nil
}

func (m *TestAgentMockDB) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	// Not needed for this specific test flow as AskProject uses Execute for search
	return nil, nil
}

func (m *TestAgentMockDB) Close() {}

func TestAskProjectAgentic_Flow(t *testing.T) {
	// 1. Setup Mock DB Data
	// When "SELECT ... FROM file_chunk" is called (triggered by SEARCH), return this.
	chunks := []map[string]interface{}{
		{
			"id":           "chunk:1",
			"path":         "/src/auth.go",
			"content":      "func Login() { // JWT logic }",
			"score":        0.95,
			"hash":         "abc",
			"current_hash": "abc",
		},
	}

	// We also need to handle the graph context enrichment if AskProject does it.
	// AskProject queries "FROM file_chunk".
	// It basically does:
	// 1. Embed (Mocked)
	// 2. Vector Search (Mocked DB returns chunks)
	// 3. GetFileContext (skipped if we don't return enough data or mock it differently, but AskProject calls GetFileContext for top 3)
	//    GetFileContext calls "SmartQuery". Our mock implementation just returns nil for SmartQuery,
	//    so context might be empty. That's fine for this test, we care about the Agentic loop finding the text.

	mockDB := &TestAgentMockDB{
		ReturnData: map[string]interface{}{
			"FROM file_chunk": chunks,
		},
	}

	// 2. Setup Mock AI
	// We need to simulate the conversation.
	// Call 1: Agent receives User Prompt -> Agent replies "THOUGHT: ... SEARCH: auth logic"
	// Call 2: Agent receives User Prompt + Agent Reply 1 + Observation (Results) -> Agent replies "FINAL ANSWER: ..."

	stepCounter := 0
	mockAI := &TestAgentMockAI{
		GenerateFunc: func(contents []ai.Content) (ai.Candidate, error) {
			stepCounter++

			// Verify input history grows
			// Call 1: System + Question
			// Call 2: System + Question + Model(Search) + User(Observation)

			if stepCounter == 1 {
				// Turn 1: Decide to search
				return ai.Candidate{
					Content: ai.Content{
						Role: "model",
						Parts: []ai.Part{
							{Text: "THOUGHT: The user asks about auth. I should check the code.\nSEARCH: auth logic"},
						},
					},
				}, nil
			} else if stepCounter == 2 {
				// Turn 2: Analyze results and answer
				// Verify that contents contain the observation
				lastMsg := contents[len(contents)-1]
				if !strings.Contains(lastMsg.Parts[0].Text, "OBSERVATION:") {
					return ai.Candidate{}, fmt.Errorf("expected OBSERVATION in history for step 2")
				}
				if !strings.Contains(lastMsg.Parts[0].Text, "JWT logic") {
					return ai.Candidate{}, fmt.Errorf("expected search results in OBSERVATION")
				}

				return ai.Candidate{
					Content: ai.Content{
						Role: "model",
						Parts: []ai.Part{
							{Text: "THOUGHT: I found the login function.\nFINAL ANSWER: Auth is handled via JWT in auth.go."},
						},
					},
				}, nil
			}

			return ai.Candidate{}, fmt.Errorf("unexpected step %d", stepCounter)
		},
	}

	svc := &Service{
		DB: mockDB,
		AI: mockAI,
	}

	// 3. Execute
	result, err := svc.AskProjectAgentic(context.Background(), "How does auth work?")
	if err != nil {
		t.Fatalf("AskProjectAgentic failed: %v", err)
	}

	// 4. Assertions
	if result.Answer != "Auth is handled via JWT in auth.go." {
		t.Errorf("Unexpected answer: %s", result.Answer)
	}

	if len(result.Steps) != 2 {
		t.Errorf("Expected 2 steps, got %d", len(result.Steps))
	}

	// Verify Search was actually called on DB
	foundSearch := false
	for _, sql := range mockDB.CapturedQueries {
		if strings.Contains(sql, "SELECT") && strings.Contains(sql, "FROM file_chunk") {
			foundSearch = true
			break
		}
	}
	if !foundSearch {
		t.Error("Database search query was not executed")
	}

	// Verify Source was added
	if len(result.Sources) != 1 {
		t.Errorf("Expected 1 source, got %d", len(result.Sources))
	} else {
		if result.Sources[0].Path != "/src/auth.go" {
			t.Errorf("Expected source /src/auth.go, got %s", result.Sources[0].Path)
		}
	}
}
