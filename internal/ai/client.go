package ai

import (
	"context"
	"errors"
	"os/exec"

	"github.com/terenzif/ibis-assistant/internal/logger"
)

var (
	errNoEmbeddingProvider = errors.New("no embedding provider configured")
	errNoReasoningProvider = errors.New("no reasoning provider configured")
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
	if c.embedding == nil {
		return nil, errNoEmbeddingProvider
	}
	return c.embedding.EmbedText(ctx, text)
}

func (c *Client) BatchEmbedText(ctx context.Context, texts []string) ([][]float32, error) {
	if c.embedding == nil {
		return nil, errNoEmbeddingProvider
	}
	return c.embedding.BatchEmbedText(ctx, texts)
}

func (c *Client) GenerateContent(ctx context.Context, contents []Content, config GenerationConfig) (Candidate, error) {
	if c.reasoning == nil {
		return Candidate{}, errNoReasoningProvider
	}
	return c.reasoning.GenerateContent(ctx, contents, config)
}

// IsFunctional returns true if both embedding and reasoning providers are configured.
// Use IsEmbeddingFunctional for operations that only require embeddings.
func (c *Client) IsFunctional() bool {
	return c.embedding != nil && c.embedding.Name() != "" && c.reasoning != nil && c.reasoning.Name() != ""
}

// IsEmbeddingFunctional returns true if the embedding provider is configured.
// This should be used for operations that only need embeddings (e.g. code ingestion)
// and do not require a reasoning (generative) model.
func (c *Client) IsEmbeddingFunctional() bool {
	return c.embedding != nil && c.embedding.Name() != ""
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
