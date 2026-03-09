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
	EmbedCalls   []string
}

func (m *TestAgentMockAI) EmbedText(ctx context.Context, text string) ([]float32, error) {
	m.EmbedCalls = append(m.EmbedCalls, text)
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
	ReturnError     error
}

func (m *TestAgentMockDB) Execute(sql string) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	if m.ReturnError != nil {
		return nil, m.ReturnError
	}
	for k, v := range m.ReturnData {
		if strings.Contains(sql, k) {
			return v, nil
		}
	}
	return nil, nil
}

func (m *TestAgentMockDB) SmartQuery(sql string, vars interface{}) (interface{}, error) {
	m.CapturedQueries = append(m.CapturedQueries, sql)
	if m.ReturnError != nil {
		return nil, m.ReturnError
	}
	for k, v := range m.ReturnData {
		if strings.Contains(sql, k) {
			return v, nil
		}
	}
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

func TestAskProjectAgentic_MixedResponse_Bug(t *testing.T) {
	// Reproduction of bug where FINAL ANSWER takes precedence even if SEARCH appears first.

	mockDB := &TestAgentMockDB{
		ReturnData: map[string]interface{}{
			"FROM file_chunk": []map[string]interface{}{}, // Return empty, doesn't matter, we want to see if Search is called
		},
	}

	stepCounter := 0
	mockAI := &TestAgentMockAI{
		GenerateFunc: func(contents []ai.Content) (ai.Candidate, error) {
			stepCounter++
			if stepCounter == 1 {
				// The model outputs SEARCH first, then implies it will give a final answer later or confusingly mixes them.
				// Current implementation checks FINAL ANSWER first.
				return ai.Candidate{
					Content: ai.Content{
						Role: "model",
						Parts: []ai.Part{
							{Text: "I will SEARCH: \"something\" to find the FINAL ANSWER: 42"},
						},
					},
				}, nil
			}
			// Should not reach here if bug exists (it will exit at step 1)
			// If bug is fixed, it should search, then we return final answer
			return ai.Candidate{
				Content: ai.Content{
					Role: "model",
					Parts: []ai.Part{
						{Text: "FINAL ANSWER: 42"},
					},
				},
			}, nil
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

	// If bug exists:
	// It sees "FINAL ANSWER: 42" in the first response.
	// It extracts "42" (or "42" depending on regex).
	// It does NOT execute search.
	// result.Steps will have 1 element.

	// If bug is fixed:
	// It sees "SEARCH: \"something\"" first.
	// It executes search.
	// It goes to step 2.
	// result.Steps will have 2 elements.

	foundSearch := false
	for _, sql := range mockDB.CapturedQueries {
		if strings.Contains(sql, "SELECT") && strings.Contains(sql, "FROM file_chunk") {
			foundSearch = true
			break
		}
	}

	if !foundSearch {
		t.Errorf("BUG REPRODUCED: Search was NOT executed. The agent likely parsed FINAL ANSWER prematurely.")
	}

	if len(result.Steps) < 2 {
		t.Errorf("BUG REPRODUCED: Expected at least 2 steps (Search -> Answer), got %d", len(result.Steps))
	}
}

func TestAskProjectAgentic_SearchError(t *testing.T) {
	// Simulate DB error during search
	mockDB := &TestAgentMockDB{
		ReturnError: fmt.Errorf("simulated db error"),
	}

	stepCounter := 0
	mockAI := &TestAgentMockAI{
		GenerateFunc: func(contents []ai.Content) (ai.Candidate, error) {
			stepCounter++
			if stepCounter == 1 {
				// 1. Agent tries to search
				return ai.Candidate{
					Content: ai.Content{
						Role: "model",
						Parts: []ai.Part{
							{Text: "SEARCH: something"},
						},
					},
				}, nil
			} else if stepCounter == 2 {
				// 2. Agent receives error observation
				lastMsg := contents[len(contents)-1]
				text := lastMsg.Parts[0].Text
				if !strings.Contains(text, "OBSERVATION: Search failed") || !strings.Contains(text, "simulated db error") {
					return ai.Candidate{}, fmt.Errorf("expected error observation, got: %s", text)
				}
				// Agent gives up or tries something else
				return ai.Candidate{
					Content: ai.Content{
						Role: "model",
						Parts: []ai.Part{
							{Text: "FINAL ANSWER: I failed."},
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

	result, err := svc.AskProjectAgentic(context.Background(), "test")
	if err != nil {
		t.Fatalf("AskProjectAgentic failed: %v", err)
	}

	if result.Answer != "I failed." {
		t.Errorf("Unexpected answer: %s", result.Answer)
	}
}

func TestAskProjectAgentic_GraphContext(t *testing.T) {
	// 1. Setup Mock DB Data
	issueID := "issue:101"

	// Vector Search Result
	chunks := []map[string]interface{}{
		{
			"id":           "chunk:1",
			"path":         "/src/context.go",
			"content":      "func Context() {}",
			"score":        0.95,
			"hash":         "abc",
			"current_hash": "abc",
		},
	}

	// Graph Context (GetFileContext)
	graphResponse := []interface{}{
		map[string]interface{}{
			"change_edges": []interface{}{},
			"history": []interface{}{
				map[string]interface{}{
					"id":      "commit:hash123",
					"hash":    "hash123",
					"message": "fix bug",
					"date":    "2024-01-01T00:00:00Z",
					"author":  []string{"dev"},
					"issues": []interface{}{
						map[string]interface{}{
							"id":      issueID,
							"subject": "Fix context bug",
							"status":  "Open",
							"weight":  []float64{0.8},
						},
					},
				},
			},
		},
	}

	mockDB := &TestAgentMockDB{
		ReturnData: map[string]interface{}{
			"FROM file_chunk": chunks,        // Triggered by AskProject Vector Search
			"<-changed":       graphResponse, // Triggered by AskProject -> GetFileContext
		},
	}

	// 2. Mock AI
	stepCounter := 0
	mockAI := &TestAgentMockAI{
		GenerateFunc: func(contents []ai.Content) (ai.Candidate, error) {
			stepCounter++

			// Step 1: Decide to Search
			if stepCounter == 1 {
				return ai.Candidate{
					Content: ai.Content{
						Role: "model",
						Parts: []ai.Part{
							{Text: "THOUGHT: I need to check context.\nSEARCH: context"},
						},
					},
				}, nil
			} else if stepCounter == 2 {
				// Step 2: Receive Observation
				// Verify that the observation contains the related issue info
				lastMsg := contents[len(contents)-1]
				text := lastMsg.Parts[0].Text

				// Check for "Context:" block or specifically the issue subject
				if !strings.Contains(text, "Fix context bug") {
					return ai.Candidate{}, fmt.Errorf("OBSERVATION missing related issue 'Fix context bug'. Got: %s", text)
				}

				return ai.Candidate{
					Content: ai.Content{
						Role: "model",
						Parts: []ai.Part{
							{Text: "FINAL ANSWER: Found it."},
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
	_, err := svc.AskProjectAgentic(context.Background(), "test")
	if err != nil {
		// If the mock AI returns an error (due to missing context), AskProjectAgentic wraps it.
		// We expect this to happen if the bug exists.
		if strings.Contains(err.Error(), "OBSERVATION missing related issue") {
			t.Logf("Successfully reproduced bug: %v", err)
			// For TDD, we want to FAIL the test if the bug is present, so that fixing it makes the test PASS.
			t.Fatalf("Test failed as expected (Bug Reproduced): %v", err)
		}
		t.Fatalf("AskProjectAgentic failed unexpectedly: %v", err)
	}
}
