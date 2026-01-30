package ai

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
)

const (
	EmbeddingModel = "models/gemini-embedding-001"
	TableKeyUsage  = "key_usage"
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
	TPM   int
	RPD   int
	Owner string
}

type worker struct {
	apiKey        string
	owner         string
	client        *http.Client
	dbClient      db.Executor
	costInterval  time.Duration // Time to wait per 1 item of cost
	nextAvailable time.Time

	// TPM (Tokens Per Minute)
	limitTPM     int
	usedTPM      int
	lastResetTPM time.Time

	// RPD (Requests Per Day)
	limitRPD     int
	usedRPD      int
	lastResetRPD time.Time

	usageID      string // Cached DB ID for today

	mu sync.Mutex
}

func NewClient(apiKeys []KeyConfig, dbClient db.Executor) *Client {
	if len(apiKeys) == 0 {
		return &Client{}
	}

	workers := make([]*worker, len(apiKeys))

	for i, cfg := range apiKeys {
		// Calculate how much time each "item" costs based on RPM.
		var costInterval time.Duration
		if cfg.RPM <= 0 {
			costInterval = 0 // No limit
		} else {
			costInterval = time.Minute / time.Duration(cfg.RPM)
		}

		w := &worker{
			apiKey:       cfg.Key,
			owner:        cfg.Owner,
			client:       &http.Client{Timeout: 30 * time.Second},
			dbClient:     dbClient,
			costInterval: costInterval,
			limitTPM:     cfg.TPM,
			limitRPD:     cfg.RPD,
			lastResetTPM: time.Now(),
			lastResetRPD: time.Now(),
		}

		// Determine DB ID for usage tracking
		w.updateUsageID()

		// Synchronously load usage (blocking slightly at startup is safer than racing)
		w.loadUsageFromDB()

		workers[i] = w
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

	// Estimate token cost (conservative estimate: 1 token ~ 4 chars)
	estimatedTokens := 0
	for _, t := range texts {
		estimatedTokens += len(t) / 4
		if len(t) > 0 {
			estimatedTokens++ // Minimum 1 token
		}
	}

	// Retry loop (handled by Client orchestration)
	for {
		// 1. Find an available worker
		var selectedWorker *worker
		var earliestAvailable time.Time
		now := time.Now()

		for _, w := range c.workers {
			w.mu.Lock()

			// Check Day boundary for RPD
			if now.Format("2006-01-02") != w.lastResetRPD.Format("2006-01-02") {
				w.usedRPD = 0 // Reset local counter for new day
				w.lastResetRPD = now
				w.updateUsageID() // New DB ID
			}

			// Reset TPM counters if minute window passed
			if now.Sub(w.lastResetTPM) >= time.Minute {
				w.usedTPM = 0
				w.lastResetTPM = now
			}

			// --- Check Limits ---

			// 1. RPD (Hard limit, skip worker if exceeded)
			// One HTTP request for the batch
			if w.limitRPD > 0 && w.usedRPD+1 > w.limitRPD {
				w.mu.Unlock()
				continue // Worker exhausted for the day
			}

			// 2. TPM (Skip if exceeded for this minute, or wait)
			tpmExceeded := false
			if w.limitTPM > 0 && w.usedTPM+estimatedTokens > w.limitTPM {
				tpmExceeded = true
			}

			// 3. RPM (Time based availability)
			rpmAvailable := !now.Before(w.nextAvailable)

			// Decision Logic
			if !tpmExceeded && rpmAvailable {
				// Available now!
				w.nextAvailable = now.Add(w.costInterval * time.Duration(cost))
				selectedWorker = w
				w.mu.Unlock()
				break
			} else {
				// Calculate wait time
				var waitTime time.Time

				// Wait for RPM?
				if w.nextAvailable.After(waitTime) {
					waitTime = w.nextAvailable
				}

				// Wait for TPM? (Start of next minute window)
				if tpmExceeded {
					nextMin := w.lastResetTPM.Add(time.Minute)
					if nextMin.After(waitTime) {
						waitTime = nextMin
					}
				}

				// Keep track of earliest available for waiting
				if earliestAvailable.IsZero() || waitTime.Before(earliestAvailable) {
					earliestAvailable = waitTime
				}
			}
			w.mu.Unlock()
		}

		// 2. If no worker available, wait
		if selectedWorker == nil {
			if earliestAvailable.IsZero() {
				// If earliestAvailable is zero, it means all workers are RPD exhausted
				// or no workers configured.
				return nil, fmt.Errorf("all keys exhausted daily quotas (RPD) or unavailable")
			}
			wait := time.Until(earliestAvailable)
			if wait > 0 {
				logger.Debug("AI: All keys busy/limited, waiting %v...", wait)
				time.Sleep(wait)
			}
			continue // Retry selection
		}

		// 3. Execute Request
		res, retryDelay, err := selectedWorker.doEmbed(texts)
		if err == nil {
			// Update Usage Stats on Success
			selectedWorker.mu.Lock()
			selectedWorker.usedTPM += estimatedTokens
			selectedWorker.usedRPD += 1 // 1 HTTP Request

			// Fire-and-forget DB update (Pass by value to avoid race condition)
			go selectedWorker.updateDBUsage(selectedWorker.usageID, selectedWorker.usedRPD, selectedWorker.owner)

			selectedWorker.mu.Unlock()

			return res, nil
		}

		// 4. Handle Failure
		if retryDelay > 0 {
			selectedWorker.mu.Lock()
			selectedWorker.nextAvailable = time.Now().Add(retryDelay)
			selectedWorker.mu.Unlock()
			logger.Warn("AI: Worker rate limited (429). Retrying on another key... (Wait: %v)", retryDelay)
			continue
		}

		// Genuine error
		return nil, err
	}
}

func (w *worker) updateUsageID() {
	// ID: key_usage:<hash>_<date>
	h := sha256.New()
	h.Write([]byte(w.apiKey))
	hash := hex.EncodeToString(h.Sum(nil))[:8] // Short hash
	date := time.Now().Format("2006-01-02")
	w.usageID = fmt.Sprintf("%s:%s_%s", TableKeyUsage, hash, date)
}

func (w *worker) loadUsageFromDB() {
	if w.dbClient == nil {
		return
	}
	// Select requests
	ql := fmt.Sprintf("SELECT requests FROM %s;", w.usageID)
	res, err := w.dbClient.Execute(ql)
	if err == nil {
		// Parse result. Expecting []interface{} -> map -> requests
		if rows, ok := res.([]interface{}); ok && len(rows) > 0 {
			if row, ok := rows[0].(map[string]interface{}); ok {
				if val, ok := row["requests"].(float64); ok {
					w.usedRPD = int(val)
					logger.Info("AI: Loaded RPD usage for %s (%s): %d/%d", w.owner, w.usageID, w.usedRPD, w.limitRPD)
				}
			}
		}
	}
}

func (w *worker) updateDBUsage(id string, count int, owner string) {
	if w.dbClient == nil {
		return
	}

	// Using SmartQuery to prevent SQL Injection and handle parameters safely.
	// Note: record ID handling in parameters can be driver specific,
	// so we construct the ID string safely (it's a hash, so it's safe-ish, but let's be strict).
	// We will try UPDATE first, then CREATE.

	// Update
	updateQL := fmt.Sprintf("UPDATE %s SET requests = $req, last_updated = time::now();", id)
	vars := map[string]interface{}{
		"req": count,
	}

	_, err := w.dbClient.SmartQuery(updateQL, vars)
	if err != nil {
		// If update failed (likely doesn't exist), try CREATE
		createQL := fmt.Sprintf("CREATE %s SET requests = $req, owner = $owner, date = time::now();", id)
		createVars := map[string]interface{}{
			"req":   count,
			"owner": owner,
		}
		w.dbClient.SmartQuery(createQL, createVars)
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
