package optimization

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/deckonline/knowledge_mcp/internal/ai"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/deckonline/knowledge_mcp/internal/search"
)

// Optimizer manages the Retrieval-Augmented Fine-Tuning (RAFT) self-optimization loop, improving knowledge graph relevance over time.
type Optimizer struct {
	DB     db.Executor
	AI     search.AIProvider // Use the interface from search package
	Search *search.Service
}

func NewOptimizer(db db.Executor, ai search.AIProvider, s *search.Service) *Optimizer {
	return &Optimizer{
		DB:     db,
		AI:     ai,
		Search: s,
	}
}

// GenerateSyntheticQA uses the LLM to create a question that the given code chunk answers.
func (o *Optimizer) GenerateSyntheticQA(ctx context.Context, chunkContent string) (string, error) {
	// Truncate chunk if too large to avoid token waste
	if len(chunkContent) > 3000 {
		chunkContent = chunkContent[:3000] + "...(truncated)"
	}

	prompt := fmt.Sprintf(`You are a technical examiner.
I will provide a code snippet. Your task is to generate a specific, technical question that a developer would ask, which is uniquely answered by this snippet.
The question should not mention the file name or variable names directly unless they are critical concepts.

SNIPPET:
%s

OUTPUT FORMAT:
QUESTION: <your question>
`, chunkContent)

	resp, err := o.AI.GenerateContent(ctx, []ai.Content{{Parts: []ai.Part{{Text: prompt}}}}, ai.GenerationConfig{Temperature: 0.7})
	if err != nil {
		return "", err
	}

	if resp.UsageMetadata != nil && o.DB != nil {
		tracker := ai.NewCostTracker(o.DB)
		tracker.RecordUsage(ctx, "system:optimizer_raft", resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount)
	}

	text := ""
	for _, p := range resp.Content.Parts {
		text += p.Text
	}

	// Parse
	if strings.Contains(text, "QUESTION:") {
		parts := strings.Split(text, "QUESTION:")
		if len(parts) > 1 {
			return strings.TrimSpace(parts[1]), nil
		}
	}
	return strings.TrimSpace(text), nil
}

// EvaluateRetrieval runs a search and checks if the expected chunk ID is in the top results.
func (o *Optimizer) EvaluateRetrieval(ctx context.Context, question string, expectedChunkID string) (float64, error) {
	results, err := o.Search.AskProject(ctx, question)
	if err != nil {
		return 0, err
	}

	for i, r := range results {
		if r.ID == expectedChunkID {
			// Score based on rank: 1.0 for Top 1, 0.5 for Top 2, etc.
			return 1.0 / float64(i+1), nil
		}
	}

	return 0, nil
}

// OptimizeLoop executes the optimization process for a defined number of iterations, identifying and reinforcing valuable connections.
func (o *Optimizer) OptimizeLoop(ctx context.Context, iterations int) error {
	if o.DB == nil {
		return fmt.Errorf("database not connected")
	}
	logger.Info("Starting Optimization Loop for %d iterations...", iterations)

	// 1. Select Candidates (Random)
	ql := fmt.Sprintf("SELECT id, content FROM file_chunk ORDER BY rand() LIMIT %d;", iterations)
	res, err := o.DB.Execute(ctx, ql)
	if err != nil {
		return fmt.Errorf("failed to select chunks: %w", err)
	}

	// Parse
	type Chunk struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	var chunks []Chunk
	bytes, _ := json.Marshal(res)
	if err := json.Unmarshal(bytes, &chunks); err != nil {
		return fmt.Errorf("failed to parse chunks: %w", err)
	}

	for i, c := range chunks {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		logger.Debug("Optimization [%d/%d]: Processing chunk %s", i+1, len(chunks), c.ID)

		// 2. Generate Question
		question, err := o.GenerateSyntheticQA(ctx, c.Content)
		if err != nil {
			logger.Warn("Optimization: QA gen failed for %s: %v", c.ID, err)
			continue
		}
		if question == "" {
			continue
		}

		logger.Debug("Optimization: Generated Question: %s", question)

		// 3. Evaluate
		score, err := o.EvaluateRetrieval(ctx, question, c.ID)
		if err != nil {
			logger.Warn("Optimization: Eval failed: %v", err)
			continue
		}

		logger.Info("Optimization: Chunk %s scored %.2f", c.ID, score)

		// 4. Reinforce
		if score > 0 {
			// Found! Reinforce the node to signal it's valuable information.
			ql := fmt.Sprintf("UPDATE %s SET access_count = (access_count OR 0) + 1, last_accessed = time::now();", c.ID)
			if _, err := o.DB.Execute(ctx, ql); err != nil {
				logger.Warn("Optimization: Reinforce failed: %v", err)
			}
		} else {
			// Missed.
			logger.Warn("Optimization: MISS. Question '%s' did not retrieve %s.", question, c.ID)

			// Future: Add synthetic link/concept
		}
	}

	logger.Info("Optimization Loop Complete.")
	return nil
}
