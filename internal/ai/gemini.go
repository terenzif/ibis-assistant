package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
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
}

type KeyConfig struct {
	Key   string
	RPM   int
	Owner string
}

type worker struct {
	apiKey        string
	owner         string
	client        *http.Client
	costInterval  time.Duration // Time to wait per 1 item of cost
	nextAvailable time.Time
	mu            sync.Mutex
}

func NewClient(apiKeys []KeyConfig) *Client {
	if len(apiKeys) == 0 {
		return &Client{}
	}

	workers := make([]*worker, len(apiKeys))

	for i, cfg := range apiKeys {
		// Calculate how much time each "item" costs.
		// If RPM=60, then 60 items per minute.
		// 1 item = 1 second.
		var costInterval time.Duration
		if cfg.RPM <= 0 {
			costInterval = 0 // No limit
		} else {
			costInterval = time.Minute / time.Duration(cfg.RPM)
		}

		workers[i] = &worker{
			apiKey:       cfg.Key,
			owner:        cfg.Owner,
			client:       &http.Client{Timeout: 30 * time.Second},
			costInterval: costInterval,
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
// It implements advanced rate limiting and failover across keys.
func (c *Client) BatchEmbedText(texts []string) ([][]float32, error) {
	if len(c.workers) == 0 {
		return nil, fmt.Errorf("no ai worker available (check API keys)")
	}

	cost := len(texts)
	if cost == 0 {
		return [][]float32{}, nil
	}

	// Retry loop (handled by Client orchestration)
	for {
		// 1. Find an available worker
		var selectedWorker *worker
		var earliestAvailable time.Time
		now := time.Now()

		for _, w := range c.workers {
			w.mu.Lock()
			if now.After(w.nextAvailable) || now.Equal(w.nextAvailable) {
				// Found available!
				// Reserve the time slot
				// We advance nextAvailable by the cost of this request.
				w.nextAvailable = now.Add(w.costInterval * time.Duration(cost))
				selectedWorker = w
				w.mu.Unlock()
				break
			} else {
				// Keep track of earliest available for waiting
				if earliestAvailable.IsZero() || w.nextAvailable.Before(earliestAvailable) {
					earliestAvailable = w.nextAvailable
				}
			}
			w.mu.Unlock()
		}

		// 2. If no worker available, wait
		if selectedWorker == nil {
			if earliestAvailable.IsZero() {
				// Should not happen unless no workers
				return nil, fmt.Errorf("no workers configured")
			}
			wait := time.Until(earliestAvailable)
			if wait > 0 {
				logger.Debug("AI: All keys busy, waiting %v...", wait)
				time.Sleep(wait)
			}
			continue // Retry selection
		}

		// 3. Execute Request
		res, retryDelay, err := selectedWorker.doEmbed(texts)
		if err == nil {
			return res, nil
		}

		// 4. Handle Failure
		// If it's a 429 (Rate Limit), mark this worker as busy and retry loop
		if retryDelay > 0 {
			selectedWorker.mu.Lock()
			// Push availability into the future
			selectedWorker.nextAvailable = time.Now().Add(retryDelay)
			selectedWorker.mu.Unlock()
			logger.Warn("AI: Worker rate limited (429). Retrying on another key... (Wait: %v)", retryDelay)
			continue // Loop will pick another worker
		}

		// Genuine error
		return nil, err
	}
}

// doEmbed performs the actual HTTP request. Returns (result, retryDelay, error).
// It does NOT modify nextAvailable or sleep.
func (w *worker) doEmbed(texts []string) ([][]float32, time.Duration, error) {
	keyInfo := w.apiKey
	if len(keyInfo) > 8 {
		keyInfo = keyInfo[len(keyInfo)-4:]
	}
	ownerInfo := w.owner
	if ownerInfo == "" {
		ownerInfo = "unknown"
	}
	logger.Debug("AI: Sending batch embedding request (size: %d) using key ...%s (%s)", len(texts), keyInfo, ownerInfo)

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
		return nil, 0, err
	}

	start := time.Now()
	resp, err := w.client.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	duration := time.Since(start)

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 200 {
		logger.Debug("AI: Successfully received embeddings for %d items in %v", len(texts), duration)
		var result BatchEmbedResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, 0, fmt.Errorf("parsing error: %w", err)
		}
		// Extract values
		out := make([][]float32, len(result.Embeddings))
		for i, e := range result.Embeddings {
			out[i] = e.Values
		}
		return out, 0, nil
	}

	if resp.StatusCode == 429 {
		retryDelay := parseRetryDelay(body)
		if retryDelay == 0 {
			// Default backoff if parsing fails but 429 is present
			retryDelay = 5 * time.Second
		}
		return nil, retryDelay, fmt.Errorf("rate limit exceeded")
	}

	return nil, 0, fmt.Errorf("gemini api error %d (key ...%s): %s", resp.StatusCode, keyInfo, string(body))
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
