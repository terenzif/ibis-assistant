package search

import (
	"encoding/json"
	"fmt"
	"strings"

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
	ID        string      `json:"code_chunk_id"`
	Path      string      `json:"file_path"`
	Score     float64     `json:"relevance_score"`
	IsCurrent bool        `json:"is_current"` // Placeholder for HEAD check
	Context   ContextData `json:"context"`
	Content   string      `json:"content,omitempty"` // Keeping content for debug/display
}

type ContextData struct {
	RelatedIssues []IssueSummary `json:"related_issues"`
	ExpertAuthors []string       `json:"expert_authors"`
}

// AskProject performs a hybrid search:
// 1. Vector Search with Time Decay.
// 2. Graph Traversal (Weighted).
func (s *Service) AskProject(query string) ([]Result, error) {
	// 1. Embed Query
	vec, err := s.AI.EmbedText(query)
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}
	vecJson, _ := json.Marshal(vec)

	// 2. Vector Search (Time-Decayed)
	ql := fmt.Sprintf(`
		SELECT 
			id,
			file.path as path, 
			content, 
			(vector::similarity::cosine(embedding, %s) * 0.7) +
			(math::max(0, 1 - (time::now() - (created_at OR time::now())).days / 365) * 0.3)
			as score
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
			ID:        c.ID,
			Path:      c.Path,
			Score:     c.Score,
			Content:   c.Content,
			IsCurrent: true, // Logic to check if file deleted in HEAD is hard without checking FS or Git. Assume true for now.
			Context: ContextData{
				RelatedIssues: []IssueSummary{},
				ExpertAuthors: []string{},
			},
		}

		if top3[c.Path] {
			var gCtx *GraphContext
			if ctx, ok := contextCache[c.Path]; ok {
				gCtx = ctx
			} else {
				ctx, err := GetFileContext(s.DB, c.Path)
				if err == nil {
					contextCache[c.Path] = ctx
					gCtx = ctx
				}
			}

			if gCtx != nil {
				r.Context.RelatedIssues = gCtx.Issues
				// Aggregate Authors (Experts)
				authorMap := make(map[string]bool)
				for _, c := range gCtx.Commits {
					if c.Author != "" && c.Author != "Unknown" {
						authorMap[c.Author] = true
					}
				}
				for k := range authorMap {
					r.Context.ExpertAuthors = append(r.Context.ExpertAuthors, k)
				}
			}
		}
		finalResults = append(finalResults, r)
	}

	return finalResults, nil
}

// ReinforcePath updates the usage weight of a path in the graph
func (s *Service) ReinforcePath(sourceID, targetID string, score float64) error {
	// Logic:
	// 1. Find edge between source and target.
	//    We assume a specific edge type or just any edge?
	//    The spec says `implements` edge mostly.
	//    Let's try to update `implements` first.
	//    If score > 0: weight += 0.1
	//    If score < 0: weight -= 0.1

	delta := 0.1
	if score < 0 {
		delta = -0.1
	}

	// Helper to run update for a table
	runUpdate := func(table string) error {
		ql := fmt.Sprintf("UPDATE %s SET usage_weight = (usage_weight OR 1.0) + %f WHERE in = $source AND out = $target;", table, delta)
		_, err := s.DB.SmartQuery(ql, map[string]interface{}{
			"source": sourceID,
			"target": targetID,
		})
		return err
	}

	// Try 'implements' (Commit -> Issue)
	var err error
	if strings.Contains(sourceID, "commit") && strings.Contains(targetID, "issue") {
		err = runUpdate(schema.EdgeImplements)
	} else if strings.Contains(sourceID, "commit") && strings.Contains(targetID, "file") {
		// Try 'changed' (Commit -> File)
		err = runUpdate(schema.EdgeChanged)
	}

	if err != nil {
		return err
	}

	// Also update target node access_count
	// UPDATE target SET access_count += 1
	if score > 0 {
		ql := "UPDATE $target SET access_count = (access_count OR 0) + 1, last_accessed = time::now();"
		_, err = s.DB.SmartQuery(ql, map[string]interface{}{"target": targetID})
		if err != nil {
			return err
		}
	}

	return nil
}

// RawQuery executes a raw SurrealQL query for power users
func (s *Service) RawQuery(ql string) (interface{}, error) {
	return s.DB.Execute(ql)
}
