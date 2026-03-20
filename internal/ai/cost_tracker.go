package ai

import (
	"context"
	"fmt"

	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
)

type CostTracker struct {
	DB db.Executor
}

func NewCostTracker(dbClient db.Executor) *CostTracker {
	return &CostTracker{DB: dbClient}
}

func (c *CostTracker) RecordUsage(ctx context.Context, relatedEntityID string, promptTokens, candidatesTokens int) {
	if c.DB == nil {
		return
	}

	total := promptTokens + candidatesTokens
	if total == 0 {
		return
	}

	// Update the tokens directly on the entity
	// Use (tokens_used OR 0) to ensure we can increment even if it doesn't exist yet
	ql := fmt.Sprintf("UPDATE %s SET tokens_used = (tokens_used OR 0) + %d;", relatedEntityID, total)
	_, err := c.DB.Execute(ctx, ql)
	if err != nil {
		logger.Error("Failed to record token usage on %s: %v", relatedEntityID, err)
	}

	// Print cost estimate (assuming roughly $0.075 per 1M tokens for Flash 1.5, blending input/output)
	// We use 0.15 for simplicity representing the sum
	cost := float64(total) / 1_000_000.0 * 0.15
	if cost > 0.0001 {
		logger.Debug("Tokens used: %d (Cost: ~$%.5f) for %s", total, cost, relatedEntityID)
	}
}

// RecordEmbeddingUsage estimates tokens for an embedding request and records it
func (c *CostTracker) RecordEmbeddingUsage(ctx context.Context, relatedEntityID string, text string) {
	if c.DB == nil {
		return
	}
	estimatedTokens := len(text) / 3 // Rough estimate matching our rate limiter
	if len(text) > 0 {
		estimatedTokens++
	}
	c.RecordUsage(ctx, relatedEntityID, estimatedTokens, 0)
}
