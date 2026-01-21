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

// BatchEmbedRequest for batchEmbedContents
type BatchEmbedRequest struct {
	Requests []EmbedRequestItem `json:"requests"`
}
type EmbedRequestItem struct {
	Model   string  `json:"model"`
	Content Content `json:"content"`
}

type BatchEmbedResponse struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
}

// EmbedText generates a vector embedding for the given text
func (c *Client) EmbedText(text string) ([]float32, error) {
	// Re-use batch for single
	res, err := c.BatchEmbedText([]string{text})
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return res[0], nil
}

// BatchEmbedText generates embeddings for multiple strings in one call
func (c *Client) BatchEmbedText(texts []string) ([][]float32, error) {
	// Wait for rate limiter (once per batch call)
	<-c.Ticker.C

	url := fmt.Sprintf("%s/%s:batchEmbedContents?key=%s", BaseURL, EmbeddingModel, c.APIKey)

	reqItems := make([]EmbedRequestItem, len(texts))
	for i, t := range texts {
		reqItems[i] = EmbedRequestItem{
			Model: EmbeddingModel,
			Content: Content{
				Parts: []Part{{Text: t}},
			},
		}
	}
	
	payload := BatchEmbedRequest{Requests: reqItems}
	
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

	var result BatchEmbedResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing error: %w", err)
	}

	out := make([][]float32, len(result.Embeddings))
	for i, e := range result.Embeddings {
		out[i] = e.Values
	}

	return out, nil
}

