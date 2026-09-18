package search

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/terenzif/ibis-assistant/internal/ai"
	"github.com/terenzif/ibis-assistant/internal/db"
	"github.com/terenzif/ibis-assistant/internal/schema"
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

	if s.DB == nil {
		return nil, fmt.Errorf("database not connected")
	}

	tracker := ai.NewCostTracker(s.DB)
	tracker.RecordEmbeddingUsage(ctx, "system:search_vector", query)

	vecJson, _ := json.Marshal(vec)

	// 2. Vector Search (Time-Decayed) on file_chunk, memory, and reasoning.
	// SurrealDB multi-table FROM [a,b,c] returns empty for vector queries here,
	// so we query each table separately and merge in Go.
	type ChunkResult struct {
		ID          string
		Path        string
		Content     string
		Score       float64
		Hash        string
		CurrentHash string
	}

	scoreExpr := fmt.Sprintf(`(vector::similarity::cosine(embedding, %s) * 0.7) +
			(IF created_at IS NONE THEN 0.3 ELSE math::max(0, 1 - duration::days(time::now() - created_at) / 365) * 0.3 END)`, string(vecJson))

	type tableQuery struct {
		name string
		ql   string
	}
	queries := []tableQuery{
		{
			name: schema.TableFileChunk,
			ql: fmt.Sprintf(`
				SELECT id, file.path as path, content, hash, file.hash as current_hash, %s as score
				FROM %s WHERE embedding != NONE ORDER BY score DESC LIMIT 15;`,
				scoreExpr, schema.TableFileChunk),
		},
		{
			name: schema.TableMemory,
			ql: fmt.Sprintf(`
				SELECT id, NONE as path, content, NONE as hash, NONE as current_hash, %s as score
				FROM %s WHERE embedding != NONE ORDER BY score DESC LIMIT 15;`,
				scoreExpr, schema.TableMemory),
		},
		{
			name: schema.TableReasoning,
			ql: fmt.Sprintf(`
				SELECT id, NONE as path,
					string::concat(question OR '', '\n', outcome OR '') as content,
					NONE as hash, NONE as current_hash, %s as score
				FROM %s WHERE embedding != NONE ORDER BY score DESC LIMIT 15;`,
				scoreExpr, schema.TableReasoning),
		},
	}

	var chunks []ChunkResult
	for _, tq := range queries {
		resRaw, err := s.DB.Execute(ctx, tq.ql)
		if err != nil {
			return nil, fmt.Errorf("vector search failed on %s: %w", tq.name, err)
		}
		rows := normalizeRows(resRaw)
		if rows == nil {
			if resRaw != nil {
				return nil, fmt.Errorf("failed to parse chunks from %s: unexpected result type %T", tq.name, resRaw)
			}
			continue
		}
		for _, raw := range rows {
			row, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			c := ChunkResult{
				ID: db.CoerceRecordID(row["id"]),
			}
			if content, ok := row["content"].(string); ok {
				c.Content = content
			}
			if p, ok := row["path"].(string); ok {
				c.Path = p
			}
			if h, ok := row["hash"].(string); ok {
				c.Hash = h
			}
			if h, ok := row["current_hash"].(string); ok {
				c.CurrentHash = h
			}
			switch score := row["score"].(type) {
			case float64:
				c.Score = score
			case float32:
				c.Score = float64(score)
			case json.Number:
				c.Score, _ = score.Float64()
			}
			if c.ID == "" && c.Content == "" {
				continue
			}
			chunks = append(chunks, c)
		}
	}

	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].Score > chunks[j].Score
	})
	if len(chunks) > 15 {
		chunks = chunks[:15]
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
		if i >= 3 {
			break
		}
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
			if ctxVal, ok := contextCache[c.Path]; ok {
				gCtx = ctxVal
			} else {
				gCtxVal, err := GetFileContext(ctx, s.DB, c.Path)
				if err == nil {
					contextCache[c.Path] = gCtxVal
					gCtx = gCtxVal
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

// AddCollaborativeMemory inserts a collaborative memory.
func (s *Service) AddCollaborativeMemory(ctx context.Context, repoName, memoryText string, precomputedEmbedding []float64) error {
	if s.DB == nil {
		return fmt.Errorf("database not connected")
	}

	var vec []float32
	if len(precomputedEmbedding) > 0 {
		vec = make([]float32, len(precomputedEmbedding))
		for i, v := range precomputedEmbedding {
			vec[i] = float32(v)
		}
	} else {
		var err error
		vec, err = s.AI.EmbedText(ctx, memoryText)
		if err != nil {
			return fmt.Errorf("embedding failed: %w", err)
		}
	}

	repoID := db.FormatRecordID(schema.TableRepo, db.SanitizeID(repoName))

	vecJson, _ := json.Marshal(vec)
	ql := fmt.Sprintf(`
		BEGIN TRANSACTION;
		LET $mem = CREATE %s SET content = '%s', embedding = %s;
		RELATE %s->%s->$mem;
		COMMIT TRANSACTION;
	`, schema.TableMemory, db.EscapeSQL(memoryText), string(vecJson), repoID, schema.EdgeHasMemory)

	_, err := s.DB.Execute(ctx, ql)
	return err
}

// SaveReasoningOutcome saves a semantic deduction and reinforces paths.
func (s *Service) SaveReasoningOutcome(ctx context.Context, repoName, question, outcomeText string, usefulSources []string) error {
	if s.DB == nil {
		return fmt.Errorf("database not connected")
	}

	// 1. Semantic Loop: Embed and save reasoning
	fullText := "Question: " + question + "\nOutcome: " + outcomeText
	vec, err := s.AI.EmbedText(ctx, fullText)
	if err != nil {
		return fmt.Errorf("embedding failed: %w", err)
	}

	repoID := db.FormatRecordID(schema.TableRepo, db.SanitizeID(repoName))
	vecJson, _ := json.Marshal(vec)

	// Prepare structural loop commands
	var sb strings.Builder
	sb.WriteString("BEGIN TRANSACTION;\n")
	sb.WriteString(fmt.Sprintf("LET $reas = CREATE %s SET question = '%s', outcome = '%s', embedding = %s;\n",
		schema.TableReasoning, db.EscapeSQL(question), db.EscapeSQL(outcomeText), string(vecJson)))
	sb.WriteString(fmt.Sprintf("RELATE %s->%s->$reas;\n", repoID, schema.EdgeHasReasoning))

	// 2. Structural Loop: Reinforce useful sources
	delta := 0.2 // Positive reinforcement delta
	for _, sourceID := range usefulSources {
		sourceID = strings.TrimSpace(sourceID)
		if !db.IsSafeRecordID(sourceID) {
			continue
		}
		// Increment usage_weight for related edges pointing to this source
		// And increment access_count on the source itself
		sb.WriteString(fmt.Sprintf("UPDATE %s SET usage_weight = (usage_weight OR 1.0) + %f WHERE out = %s;\n", schema.EdgeChanged, delta, sourceID))
		sb.WriteString(fmt.Sprintf("UPDATE %s SET usage_weight = (usage_weight OR 1.0) + %f WHERE out = %s;\n", schema.EdgeImplements, delta, sourceID))
		sb.WriteString(fmt.Sprintf("UPDATE %s SET access_count = (access_count OR 0) + 1, last_accessed = time::now();\n", sourceID))
	}
	sb.WriteString("COMMIT TRANSACTION;\n")

	_, err = s.DB.Execute(ctx, sb.String())
	return err
}

// normalizeRows accepts Surreal driver / mock result shapes as a []interface{} of row maps.
func normalizeRows(resRaw interface{}) []interface{} {
	switch rows := resRaw.(type) {
	case []interface{}:
		return rows
	case []map[string]interface{}:
		out := make([]interface{}, len(rows))
		for i := range rows {
			out[i] = rows[i]
		}
		return out
	default:
		return nil
	}
}

// RawQuery executes a raw SurrealQL query for power users
func (s *Service) RawQuery(ctx context.Context, ql string) (interface{}, error) {
	if s.DB == nil {
		return nil, fmt.Errorf("database not connected")
	}
	return s.DB.Execute(ctx, ql)
}
