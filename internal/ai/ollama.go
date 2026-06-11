package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OllamaProvider implements the EmbeddingProvider interface for local Ollama instances.
type OllamaProvider struct {
	model  string
	apiURL string
	client *http.Client
}

type ollamaEmbedRequest struct {
	Model string      `json:"model"`
	Input interface{} `json:"input"` // Can be string or []string
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// NewOllamaProvider creates a new Ollama client for embeddings.
func NewOllamaProvider(model, apiURL string) *OllamaProvider {
	if apiURL == "" {
		apiURL = "http://127.0.0.1:11434"
	}
	return &OllamaProvider{
		model:  model,
		apiURL: apiURL,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Name returns the provider name.
func (o *OllamaProvider) Name() string {
	return "ollama"
}

// IsFunctional returns true if the provider is configured.
func (o *OllamaProvider) IsFunctional() bool {
	return o.apiURL != ""
}

// Stop cleans up any resources.
func (o *OllamaProvider) Stop() {
	// Nothing to clean up
}

// EmbedText returns the embedding for a single text.
func (o *OllamaProvider) EmbedText(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := o.BatchEmbedText(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned from Ollama")
	}
	return embeddings[0], nil
}

// BatchEmbedText returns embeddings for multiple texts.
func (o *OllamaProvider) BatchEmbedText(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	url := fmt.Sprintf("%s/api/embed", o.apiURL)
	reqBody := ollamaEmbedRequest{
		Model: o.model,
		Input: texts,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Ollama embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create Ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var respData ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return nil, fmt.Errorf("failed to decode Ollama response: %w", err)
	}

	if len(respData.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama returned empty embeddings")
	}

	return respData.Embeddings, nil
}
