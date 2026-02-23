package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/deckonline/knowledge_mcp/internal/ai"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

// AIProvider interface for AI client
type AIProvider interface {
	EmbedText(ctx context.Context, text string) ([]float32, error)
	GenerateContent(ctx context.Context, contents []ai.Content, config ai.GenerationConfig) (ai.Candidate, error)
}

// Service handles hybrid queries
type Service struct {
	DB db.Executor
	AI AIProvider
}

// Result represents a knowledge chunk (Code, Commit, or Issue)
type Result struct {
	ID        string      `json:"code_chunk_id"`
	Path      string      `json:"file_path"`
	Score     float64     `json:"relevance_score"`
	IsCurrent bool        `json:"is_current"` // True if this chunk belongs to the current version of the file
	Context   ContextData `json:"context"`
	Content   string      `json:"content,omitempty"` // Keeping content for debug/display
}

type ContextData struct {
	RelatedIssues []IssueSummary  `json:"related_issues"`
	Commits       []CommitSummary `json:"commits"`
	ExpertAuthors []string        `json:"expert_authors"`
}

// AskProject performs a hybrid search:
// 1. Vector Search with Time Decay.
// 2. Graph Traversal (Weighted).
func (s *Service) AskProject(ctx context.Context, query string) ([]Result, error) {
	// 1. Embed Query
	vec, err := s.AI.EmbedText(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}
	vecJson, _ := json.Marshal(vec)

	// 2. Vector Search (Time-Decayed)
	// We select `hash` from chunk and `file.hash` to compare for currency.
	ql := fmt.Sprintf(`
		SELECT 
			id,
			file.path as path, 
			content,
			hash,
			file.hash as current_hash,
			(vector::similarity::cosine(embedding, %s) * 0.7) +
			(math::max(0, 1 - (time::now() - (created_at OR time::now())).days / 365) * 0.3)
			as score
		FROM %s 
		WHERE embedding != NONE
		ORDER BY score DESC 
		LIMIT 10;`, string(vecJson), schema.TableFileChunk)

	resRaw, err := s.DB.Execute(ql)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	// Parse chunks
	type ChunkResult struct {
		ID          string  `json:"id"`
		Path        string  `json:"path"`
		Content     string  `json:"content"`
		Score       float64 `json:"score"`
		Hash        string  `json:"hash"`
		CurrentHash string  `json:"current_hash"`
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
		isCurrent := (c.Hash == c.CurrentHash)
		// If both are empty (legacy data), assume current? Or false?
		// If current_hash is set but chunk hash is empty -> false (legacy chunk vs new file state).
		if c.CurrentHash == "" {
			isCurrent = true // Legacy/Fallback
		}

		r := Result{
			ID:        c.ID,
			Path:      c.Path,
			Score:     c.Score,
			Content:   c.Content,
			IsCurrent: isCurrent,
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
				r.Context.Commits = gCtx.Commits
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

	// Extract table names
	sourceTable := ""
	if idx := strings.Index(sourceID, ":"); idx != -1 {
		sourceTable = sourceID[:idx]
	}
	targetTable := ""
	if idx := strings.Index(targetID, ":"); idx != -1 {
		targetTable = targetID[:idx]
	}

	// Try 'implements' (Commit -> Issue)
	var err error
	if sourceTable == schema.TableCommit && targetTable == schema.TableIssue {
		err = runUpdate(schema.EdgeImplements)
	} else if sourceTable == schema.TableCommit && (targetTable == schema.TableFile || targetTable == schema.TableFileChunk) {
		// Try 'changed' (Commit -> File)
		err = runUpdate(schema.EdgeChanged)
	} else if sourceTable == schema.TableAuthor && targetTable == schema.TableCommit {
		// Try 'authored' (Author -> Commit)
		err = runUpdate(schema.EdgeAuthored)
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
