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

// ClaudeProvider implements ReasoningProvider via Anthropic Messages API.
type ClaudeProvider struct {
	apiKey string
	model  string
	client *http.Client
}

type claudeReq struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []claudeMsg     `json:"messages"`
	System    string          `json:"system,omitempty"`
}

type claudeMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// NewClaudeProvider creates an Anthropic Claude reasoning provider.
func NewClaudeProvider(apiKey, model string) *ClaudeProvider {
	if model == "" {
		model = "claude-sonnet-4"
	}
	return &ClaudeProvider{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *ClaudeProvider) Name() string { return "claude" }
func (p *ClaudeProvider) IsFunctional() bool {
	return strings.TrimSpace(p.apiKey) != ""
}
func (p *ClaudeProvider) Stop() {}

func (p *ClaudeProvider) GenerateContent(ctx context.Context, contents []Content, cfg GenerationConfig) (Candidate, error) {
	if !p.IsFunctional() {
		return Candidate{}, fmt.Errorf("claude not configured")
	}
	maxTok := cfg.MaxOutputTokens
	if maxTok <= 0 {
		maxTok = 4096
	}
	var system string
	msgs := make([]claudeMsg, 0, len(contents))
	for _, c := range contents {
		var b strings.Builder
		for _, part := range c.Parts {
			b.WriteString(part.Text)
		}
		role := c.Role
		switch role {
		case "system":
			system = b.String()
			continue
		case "model", "assistant":
			role = "assistant"
		default:
			role = "user"
		}
		msgs = append(msgs, claudeMsg{Role: role, Content: b.String()})
	}
	body, _ := json.Marshal(claudeReq{Model: p.model, MaxTokens: maxTok, Messages: msgs, System: system})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Candidate{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := p.client.Do(req)
	if err != nil {
		return Candidate{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Candidate{}, fmt.Errorf("claude status %d: %s", resp.StatusCode, string(raw))
	}
	var parsed claudeResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Candidate{}, err
	}
	var text strings.Builder
	for _, block := range parsed.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return Candidate{Content: Content{Role: "model", Parts: []Part{{Text: text.String()}}}, FinishReason: "STOP"}, nil
}
