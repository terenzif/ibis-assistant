package search

import (
	"encoding/json"
	"fmt"

	"github.com/deckonline/knowledge_mcp/internal/ai"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

// SearchService handles hybrid queries
type Service struct {
	DB *db.Client
	AI *ai.Client
}

// Result represents a knowledge chunk (Code, Commit, or Issue)
type Result struct {
	Type    string  `json:"type"` // "code", "commit", "issue"
	ID      string  `json:"id"`
	Content string  `json:"content"`
	Score   float64 `json:"score,omitempty"`
}

// AskProject performs a hybrid search:
// 1. Vector Search for relevant code chunks.
// 2. Graph Traversal to find recent commits/issues affecting those files.
func (s *Service) AskProject(query string) ([]Result, error) {
	// ... (impl)
	// 3. Generate Query Embedding
	vec, err := s.AI.EmbedText(query)
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}
	vecJson, _ := json.Marshal(vec)

	// 2. Vector Search (Find relevant code)
	ql := fmt.Sprintf(`
		SELECT 
			file.path as path, 
			content, 
			vector::similarity::cosine(embedding, %s) as score 
		FROM %s 
		ORDER BY score DESC 
		LIMIT 5;`, string(vecJson), schema.TableFileChunk)

	_, err = s.DB.Execute(ql) // Ignore result for now since we can't parse easily
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}
	
	// Return placeholder
	return []Result{}, nil
}

// RawQuery executes a raw SurrealQL query for power users
func (s *Service) RawQuery(ql string) (interface{}, error) {
	return s.DB.Execute(ql)
}
