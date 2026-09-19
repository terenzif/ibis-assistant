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

// OpenAICompatProvider implements ReasoningProvider for OpenAI-compatible chat APIs.
type OpenAICompatProvider struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

type openAIChatReq struct {
	Model       string            `json:"model"`
	Messages    []openAIChatMsg   `json:"messages"`
	Temperature float64           `json:"temperature,omitempty"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
}

type openAIChatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResp struct {
	Choices []struct {
		Message openAIChatMsg `json:"message"`
	} `json:"choices"`
}

// NewOpenAICompatProvider creates a provider. baseURL should include /v1 or full prefix.
func NewOpenAICompatProvider(baseURL, apiKey, model string) *OpenAICompatProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAICompatProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *OpenAICompatProvider) Name() string { return "openai_compat" }
func (p *OpenAICompatProvider) IsFunctional() bool {
	return strings.TrimSpace(p.apiKey) != "" && strings.TrimSpace(p.model) != ""
}
func (p *OpenAICompatProvider) Stop() {}

func (p *OpenAICompatProvider) GenerateContent(ctx context.Context, contents []Content, cfg GenerationConfig) (Candidate, error) {
	if !p.IsFunctional() {
		return Candidate{}, fmt.Errorf("openai_compat not configured")
	}
	msgs := make([]openAIChatMsg, 0, len(contents))
	for _, c := range contents {
		role := c.Role
		if role == "" || role == "model" {
			if role == "model" {
				role = "assistant"
			} else {
				role = "user"
			}
		}
		var b strings.Builder
		for _, part := range c.Parts {
			b.WriteString(part.Text)
		}
		msgs = append(msgs, openAIChatMsg{Role: role, Content: b.String()})
	}
	body, _ := json.Marshal(openAIChatReq{
		Model: p.model, Messages: msgs,
		Temperature: cfg.Temperature, MaxTokens: cfg.MaxOutputTokens,
	})
	url := p.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Candidate{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return Candidate{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Candidate{}, fmt.Errorf("openai_compat status %d: %s", resp.StatusCode, string(raw))
	}
	var parsed openAIChatResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Candidate{}, err
	}
	text := ""
	if len(parsed.Choices) > 0 {
		text = parsed.Choices[0].Message.Content
	}
	return Candidate{Content: Content{Role: "model", Parts: []Part{{Text: text}}}, FinishReason: "STOP"}, nil
}
