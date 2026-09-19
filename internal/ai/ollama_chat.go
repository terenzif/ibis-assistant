package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OllamaChatProvider implements ReasoningProvider via Ollama /api/chat.
type OllamaChatProvider struct {
	model  string
	apiURL string
	client *http.Client
}

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Options  map[string]any      `json:"options,omitempty"`
}

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Message ollamaChatMessage `json:"message"`
	Done    bool              `json:"done"`
}

// NewOllamaChatProvider creates a local chat reasoning provider.
func NewOllamaChatProvider(model, apiURL string) *OllamaChatProvider {
	if apiURL == "" {
		apiURL = "http://127.0.0.1:11434"
	}
	return &OllamaChatProvider{
		model:  model,
		apiURL: apiURL,
		client: &http.Client{Timeout: 180 * time.Second},
	}
}

func (o *OllamaChatProvider) Name() string       { return "ollama" }
func (o *OllamaChatProvider) IsFunctional() bool { return o.apiURL != "" && o.model != "" }
func (o *OllamaChatProvider) Stop()              {}

func (o *OllamaChatProvider) GenerateContent(ctx context.Context, contents []Content, cfg GenerationConfig) (Candidate, error) {
	msgs := make([]ollamaChatMessage, 0, len(contents))
	for _, c := range contents {
		role := c.Role
		if role == "" {
			role = "user"
		}
		var b strings.Builder
		for _, p := range c.Parts {
			b.WriteString(p.Text)
		}
		msgs = append(msgs, ollamaChatMessage{Role: role, Content: b.String()})
	}
	opts := map[string]any{}
	if cfg.Temperature > 0 {
		opts["temperature"] = cfg.Temperature
	}
	if cfg.MaxOutputTokens > 0 {
		opts["num_predict"] = cfg.MaxOutputTokens
	}
	body, err := json.Marshal(ollamaChatRequest{
		Model: o.model, Messages: msgs, Stream: false, Options: opts,
	})
	if err != nil {
		return Candidate{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.apiURL, "/")+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Candidate{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return Candidate{}, fmt.Errorf("ollama chat: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return Candidate{}, fmt.Errorf("ollama chat status %d: %s", resp.StatusCode, string(raw))
	}
	var parsed ollamaChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Candidate{}, err
	}
	return Candidate{
		Content:      Content{Role: "model", Parts: []Part{{Text: parsed.Message.Content}}},
		FinishReason: "STOP",
	}, nil
}
