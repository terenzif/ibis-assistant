package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	EmbeddingModel = "models/embedding-001"
	BaseURL        = "https://generativelanguage.googleapis.com/v1beta"
)

type Client struct {
	APIKey string
	HTTP   *http.Client
	// Simple rate limiter
	Ticker *time.Ticker
}

func NewClient(apiKey string, rpm int) *Client {
	interval := time.Minute / time.Duration(rpm)
	return &Client{
		APIKey: apiKey,
		HTTP:   &http.Client{Timeout: 30 * time.Second},
		Ticker: time.NewTicker(interval),
	}
}

type EmbeddingRequest struct {
	Model   string   `json:"model"`
	Content Content  `json:"content"`
}
type Content struct {
	Parts []Part `json:"parts"`
}
type Part struct {
	Text string `json:"text"`
}

type EmbeddingResponse struct {
	Embedding struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// EmbedText generates a vector embedding for the given text
func (c *Client) EmbedText(text string) ([]float32, error) {
	// Wait for rate limiter
	<-c.Ticker.C

	url := fmt.Sprintf("%s/%s:embedContent?key=%s", BaseURL, EmbeddingModel, c.APIKey)

	payload := EmbeddingRequest{
		Model: EmbeddingModel,
		Content: Content{
			Parts: []Part{{Text: text}},
		},
	}
	
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTP.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("gemini api error %d: %s", resp.StatusCode, string(body))
	}

	var result EmbeddingResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing error: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("gemini api error %d: %s", result.Error.Code, result.Error.Message)
	}

	return result.Embedding.Values, nil
}
