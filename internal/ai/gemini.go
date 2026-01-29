package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/logger"
)

const (
	EmbeddingModel = "models/gemini-embedding-001"
)

var BaseURL = "https://generativelanguage.googleapis.com/v1beta"

// Client abstracts interaction with the AI Provider
// It supports multiple API Keys for simple round-robin pooling.
type Client struct {
	workers []*worker
	next    uint32
}

type worker struct {
	apiKey string
	client *http.Client
	ticker *time.Ticker
}

func NewClient(apiKeys []string, rpm int) *Client {
	if len(apiKeys) == 0 {
		return &Client{}
	}

	workers := make([]*worker, len(apiKeys))

	// Distribute RPM across workers to ensure the global RPM limit is respected.
	// If RPM is 60 and we have 2 keys, each worker gets 30 RPM.
	workerRPM := rpm / len(apiKeys)
	if workerRPM < 1 {
		workerRPM = 1
	}

	interval := time.Minute / time.Duration(workerRPM)
	if rpm <= 0 {
		interval = time.Millisecond // No limit
	}

	for i, key := range apiKeys {
		workers[i] = &worker{
			apiKey: key,
			client: &http.Client{Timeout: 30 * time.Second},
			ticker: time.NewTicker(interval),
		}
	}

	return &Client{
		workers: workers,
	}
}

func (c *Client) IsFunctional() bool {
	return len(c.workers) > 0
}

// EmbedText generates a vector embedding for the given text
func (c *Client) EmbedText(text string) ([]float32, error) {
	return c.getWorker().embedText(text)
}

// BatchEmbedText generates embeddings for multiple strings in one call
func (c *Client) BatchEmbedText(texts []string) ([][]float32, error) {
	return c.getWorker().batchEmbedText(texts)
}

func (c *Client) getWorker() *worker {
	if len(c.workers) == 0 {
		return nil
	}
	idx := atomic.AddUint32(&c.next, 1)
	return c.workers[idx%uint32(len(c.workers))]
}

// --- Worker Implementation ---

func (w *worker) embedText(text string) ([]float32, error) {
	res, err := w.batchEmbedText([]string{text})
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return res[0], nil
}

func (w *worker) batchEmbedText(texts []string) ([][]float32, error) {
	if w == nil {
		return nil, fmt.Errorf("no ai worker available (check API keys)")
	}

	// Rate Limit Wait
	if w.ticker != nil {
		<-w.ticker.C
	}

	keyInfo := w.apiKey
	if len(keyInfo) > 8 {
		keyInfo = keyInfo[len(keyInfo)-4:]
	}
	logger.Debug("AI: Sending batch embedding request (size: %d) using key ...%s", len(texts), keyInfo)

	url := fmt.Sprintf("%s/%s:batchEmbedContents?key=%s", BaseURL, EmbeddingModel, w.apiKey)

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

	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		start := time.Now()
		// Create new buffer for each attempt
		resp, err := w.client.Post(url, "application/json", bytes.NewBuffer(jsonBody))
		if err != nil {
			return nil, err
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		duration := time.Since(start)

		if resp.StatusCode == 200 {
			logger.Debug("AI: Successfully received embeddings for %d items in %v", len(texts), duration)

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

		if resp.StatusCode == 429 {
			if attempt < maxRetries {
				retryDelay := parseRetryDelay(body)
				if retryDelay == 0 {
					// Exponential backoff: 2s, 4s, 8s
					retryDelay = time.Duration(2<<attempt) * time.Second
				}
				logger.Warn("AI: Rate limit exceeded (429). Retrying in %v... (Attempt %d/%d)", retryDelay, attempt+1, maxRetries)
				time.Sleep(retryDelay)
				continue
			}
			lastErr = fmt.Errorf("gemini api error 429 (key ...%s) after %d retries: %s", keyInfo, maxRetries, string(body))
			break
		}

		// Other errors - no retry
		lastErr = fmt.Errorf("gemini api error %d (key ...%s): %s", resp.StatusCode, keyInfo, string(body))
		break
	}

	return nil, lastErr
}

// --- DTOs ---

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

type ErrorResponse struct {
	Error struct {
		Code    int           `json:"code"`
		Message string        `json:"message"`
		Status  string        `json:"status"`
		Details []ErrorDetail `json:"details"`
	} `json:"error"`
}

type ErrorDetail struct {
	Type       string `json:"@type"`
	RetryDelay string `json:"retryDelay,omitempty"`
}

func parseRetryDelay(body []byte) time.Duration {
	var errResp ErrorResponse
	if err := json.Unmarshal(body, &errResp); err != nil {
		return 0
	}
	for _, d := range errResp.Error.Details {
		if d.RetryDelay != "" {
			if dur, err := time.ParseDuration(d.RetryDelay); err == nil {
				return dur
			}
		}
	}
	return 0
}

