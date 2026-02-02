package ai

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
)

const (
	EmbeddingModel = "models/gemini-embedding-001"
	TableKeyUsage  = "key_usage"
)

var BaseURL = "https://generativelanguage.googleapis.com/v1beta"

// EmbedJob represents a work item for the AI workers
type EmbedJob struct {
	Texts      []string
	ResultChan chan EmbedResult
}

// EmbedResult represents the outcome of an embedding job
type EmbedResult struct {
	Embeddings [][]float32
	Error      error
}

// Client abstracts interaction with the AI Provider using a Worker Pool
type Client struct {
	jobQueue chan EmbedJob
	dbClient db.Executor
}

type KeyConfig struct {
	Key   string
	RPM   int
	TPM   int
	RPD   int
	Owner string
}

// worker holds the state for a single API key
type worker struct {
	apiKey        string
	owner         string
	client        *http.Client
	dbClient      db.Executor
	costInterval  time.Duration // Time to wait per 1 request (based on RPM)
	nextAvailable time.Time

	// TPM (Tokens Per Minute)
	limitTPM     int
	usedTPM      int
	lastResetTPM time.Time

	// RPD (Requests Per Day)
	limitRPD     int
	usedRPD      int
	lastResetRPD time.Time

	usageID string // Cached DB ID for today
}

func NewClient(apiKeys []KeyConfig, dbClient db.Executor) *Client {
	if len(apiKeys) == 0 {
		return &Client{}
	}

	// Create a buffered channel to hold pending jobs
	// Buffer size can be adjusted, keeping it reasonable to prevent OOM but allow burst
	jobQueue := make(chan EmbedJob, 100)

	c := &Client{
		jobQueue: jobQueue,
		dbClient: dbClient,
	}

	for _, cfg := range apiKeys {
		w := newWorker(cfg, dbClient)
		go w.startLoop(jobQueue)
	}

	return c
}

func (c *Client) IsFunctional() bool {
	return c.jobQueue != nil
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

// BatchEmbedText submits a batch of texts to the worker pool and waits for the result
func (c *Client) BatchEmbedText(texts []string) ([][]float32, error) {
	if c.jobQueue == nil {
		return nil, fmt.Errorf("AI client not initialized or no keys available")
	}

	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	resultChan := make(chan EmbedResult, 1)
	job := EmbedJob{
		Texts:      texts,
		ResultChan: resultChan,
	}

	// Submit job
	c.jobQueue <- job

	// Wait for result
	result := <-resultChan
	return result.Embeddings, result.Error
}

// --- Worker Implementation ---

func newWorker(cfg KeyConfig, dbClient db.Executor) *worker {
	// Calculate how much time each request costs based on RPM.
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
		nextAvailable: time.Now(),
	}

	w.updateUsageID()
	w.loadUsageFromDB()

	return w
}

func (w *worker) startLoop(queue <-chan EmbedJob) {
	logger.Info("AI Worker started for key ...%s", w.shortKey())

	for job := range queue {
		w.processJob(job)
	}
}

func (w *worker) processJob(job EmbedJob) {
	// 1. Rate Limiting Logic
	w.waitRateLimits(job.Texts)

	// 2. Execute Request
	// Retry logic is simplified here: we retry only on 429 internally in the loop?
	// Or we just fail and let the caller handle?
	// The original code had complex retry logic searching for other workers.
	// In a worker pool, if *this* worker fails with 429, we should probably backoff and retry
	// inside this worker, because putting it back in the queue might just fail again if all keys are busy.
	// However, if one key is bad, we don't want to block the job forever if others are free.
	// But simply: "Up to n keys * 100 rpm" implies each key manages its own stream.
	// We will implement a simple retry loop for 429s on this key.

	var embeddings [][]float32
	var err error
	maxRetries := 3

	for i := 0; i <= maxRetries; i++ {
		var retryDelay time.Duration
		embeddings, retryDelay, err = w.doEmbed(job.Texts)

		if err == nil {
			break
		}

		if retryDelay > 0 {
			// Rate limit hit
			logger.Warn("AI Worker (...%s) rate limited. Waiting %v...", w.shortKey(), retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		// Non-retriable error
		break
	}

	// 3. Send Result
	job.ResultChan <- EmbedResult{
		Embeddings: embeddings,
		Error:      err,
	}
}

func (w *worker) waitRateLimits(texts []string) {
	estimatedTokens := 0
	for _, t := range texts {
		estimatedTokens += len(t) / 4
		if len(t) > 0 {
			estimatedTokens++
		}
	}

	now := time.Now()

	// Reset Counters if needed
	if now.Format("2006-01-02") != w.lastResetRPD.Format("2006-01-02") {
		w.usedRPD = 0
		w.lastResetRPD = now
		w.updateUsageID()
	}
	if now.Sub(w.lastResetTPM) >= time.Minute {
		w.usedTPM = 0
		w.lastResetTPM = now
	}

	// Check RPD (Hard Stop)
	if w.limitRPD > 0 && w.usedRPD+1 > w.limitRPD {
		// This worker is done for the day.
		// In a real system, we might want to resign from the pool or block until tomorrow.
		// For simplicity, we sleep until tomorrow? No, that blocks a thread.
		// We just sleep for a minute and retry checking (spinning slowly),
		// essentially taking this worker out of commission.
		logger.Warn("AI Worker (...%s) exhausted RPD (%d). Pausing...", w.shortKey(), w.limitRPD)
		for {
			time.Sleep(10 * time.Minute)
			now = time.Now()
			if now.Format("2006-01-02") != w.lastResetRPD.Format("2006-01-02") {
				// New Day!
				w.usedRPD = 0
				w.lastResetRPD = now
				w.updateUsageID()
				break
			}
		}
	}

	// Check TPM (Wait)
	if w.limitTPM > 0 && w.usedTPM+estimatedTokens > w.limitTPM {
		wait := w.lastResetTPM.Add(time.Minute).Sub(now)
		if wait > 0 {
			time.Sleep(wait)
		}
		// Reset after sleep
		w.usedTPM = 0
		w.lastResetTPM = time.Now()
	}

	// Check RPM (Interval)
	now = time.Now()
	if now.Before(w.nextAvailable) {
		time.Sleep(w.nextAvailable.Sub(now))
	}

	// Update State for AFTER the request (Optimistic or we update here?)
	// Let's update here to reserve the slot.
	w.nextAvailable = time.Now().Add(w.costInterval)
	w.usedTPM += estimatedTokens
	w.usedRPD++

	// Update DB (Async)
	go w.updateDBUsage(w.usageID, w.usedRPD, w.owner)
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

func (w *worker) shortKey() string {
	if len(w.apiKey) > 8 {
		return w.apiKey[len(w.apiKey)-4:]
	}
	return "????"
}

// doEmbed performs the actual HTTP request. Returns (result, retryDelay, error).
func (w *worker) doEmbed(texts []string) ([][]float32, time.Duration, error) {
	logger.Debug("AI: Worker ...%s processing batch (size: %d)", w.shortKey(), len(texts))

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
		logger.Debug("AI: Worker ...%s success (%v)", w.shortKey(), duration)
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
			retryDelay = 5 * time.Second
		}
		return nil, retryDelay, fmt.Errorf("rate limit exceeded")
	}

	return nil, 0, fmt.Errorf("gemini api error %d: %s", resp.StatusCode, string(body))
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
