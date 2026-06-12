package ai

import (
	"context"
	"os/exec"

	"github.com/terenzif/ibis-assistant/internal/logger"
)

// Client is the unified AI client orchestrator.
type Client struct {
	embedding EmbeddingProvider
	reasoning ReasoningProvider
	runner    *OllamaRunner
}

// NewClient initializes the orchestrator Client with custom providers and runner.
func NewClient(emb EmbeddingProvider, reas ReasoningProvider, runner *OllamaRunner) *Client {
	return &Client{
		embedding: emb,
		reasoning: reas,
		runner:    runner,
	}
}

func (c *Client) EmbedText(ctx context.Context, text string) ([]float32, error) {
	return c.embedding.EmbedText(ctx, text)
}

func (c *Client) BatchEmbedText(ctx context.Context, texts []string) ([][]float32, error) {
	return c.embedding.BatchEmbedText(ctx, texts)
}

func (c *Client) GenerateContent(ctx context.Context, contents []Content, config GenerationConfig) (Candidate, error) {
	return c.reasoning.GenerateContent(ctx, contents, config)
}

func (c *Client) IsFunctional() bool {
	return c.embedding != nil && c.embedding.Name() != "" && c.reasoning != nil && c.reasoning.Name() != ""
}

func (c *Client) Stop() {
	if c.runner != nil {
		c.runner.Stop()
	}
	if c.embedding != nil {
		c.embedding.Stop()
	}
	if c.reasoning != nil && interface{}(c.reasoning) != interface{}(c.embedding) {
		c.reasoning.Stop()
	}
}

// OllamaRunner wraps the local Ollama process lifecycle.
type OllamaRunner struct {
	Cmd *exec.Cmd
}

// Stop terminates the Ollama daemon process.
func (r *OllamaRunner) Stop() error {
	if r == nil || r.Cmd == nil || r.Cmd.Process == nil {
		return nil
	}
	logger.Info("Stopping local Ollama daemon...")
	return r.Cmd.Process.Kill()
}
