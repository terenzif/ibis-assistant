package search

import (
	"encoding/json"
	"fmt"

	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

// Embedder interface for AI client
type Embedder interface {
	EmbedText(text string) ([]float32, error)
}

// SearchService handles hybrid queries
type Service struct {
	DB db.Executor
	AI Embedder
}

// Result represents a knowledge chunk (Code, Commit, or Issue)
type Result struct {
	Type    string        `json:"type"` // "code", "commit", "issue"
	ID      string        `json:"id"`
	Content string        `json:"content"`
	Path    string        `json:"path,omitempty"`
	Score   float64       `json:"score,omitempty"`
	Context *GraphContext `json:"context"` // Always return object, even if empty
}

// AskProject performs a hybrid search:
// 1. Vector Search for relevant code chunks.
// 2. Graph Traversal to find recent commits/issues affecting those files.
func (s *Service) AskProject(query string) ([]Result, error) {
	// 1. Embed Query
	vec, err := s.AI.EmbedText(query)
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}
	vecJson, _ := json.Marshal(vec)

	// 2. Vector Search (Find relevant code)
	ql := fmt.Sprintf(`
		SELECT 
			id,
			file.path as path, 
			content, 
			vector::similarity::cosine(embedding, %s) as score 
		FROM %s 
		ORDER BY score DESC 
		LIMIT 10;`, string(vecJson), schema.TableFileChunk)

	resRaw, err := s.DB.Execute(ql)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	// Parse chunks
	type ChunkResult struct {
		ID      string  `json:"id"`
		Path    string  `json:"path"`
		Content string  `json:"content"`
		Score   float64 `json:"score"`
	}

	var chunks []ChunkResult
	bytes, _ := json.Marshal(resRaw)
	if err := json.Unmarshal(bytes, &chunks); err != nil {
		return nil, fmt.Errorf("failed to parse chunks: %w", err)
	}

	// 3. Identify Top 3 Distinct Files for Enrichment
	uniquePaths := []string{}
	seen := make(map[string]bool)
	for _, c := range chunks {
		if c.Path != "" && !seen[c.Path] {
			uniquePaths = append(uniquePaths, c.Path)
			seen[c.Path] = true
		}
	}

	top3 := make(map[string]bool)
	for i, p := range uniquePaths {
		if i >= 3 { break }
		top3[p] = true
	}

	// 4. Enrich and Build Results
	contextCache := make(map[string]*GraphContext)
	var finalResults []Result

	for _, c := range chunks {
		r := Result{
			Type:    "code",
			ID:      c.ID,
			Content: c.Content,
			Path:    c.Path,
			Score:   c.Score,
			// Default empty context
			Context: &GraphContext{Commits: []CommitSummary{}, Issues: []IssueSummary{}},
		}

		if top3[c.Path] {
			if ctx, ok := contextCache[c.Path]; ok {
				r.Context = ctx
			} else {
				ctx, err := GetFileContext(s.DB, c.Path)
				if err == nil {
					contextCache[c.Path] = ctx
					r.Context = ctx
				} else {
					// Log error? For now just keep empty
				}
			}
		}
		finalResults = append(finalResults, r)
	}
	
	return finalResults, nil
}

// RawQuery executes a raw SurrealQL query for power users
func (s *Service) RawQuery(ql string) (interface{}, error) {
	return s.DB.Execute(ql)
}
