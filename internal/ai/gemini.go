package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/terenzif/ibis-assistant/internal/db"
	"github.com/terenzif/ibis-assistant/internal/logger"
	"github.com/terenzif/ibis-assistant/internal/schema"
)

const (
	EmbeddingModel           = "models/text-embedding-004"
	GenerationModel          = "models/gemini-2.5-flash"
	PreferredGenerationModel = "models/gemini-2.5-pro"
)

var BaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GeminiProvider manages interactions with the Gemini AI Provider using a robust worker pool for load balancing and rate limiting.
type GeminiProvider struct {
	jobQueue      chan EmbedJob
	generateQueue chan GenerateJob
	dbClient      db.Executor
	workers       []*worker
	wg            sync.WaitGroup
	ctx           context.Context
	cancel        context.CancelFunc
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

	// Token Bucket for TPM
	tpmBucket  float64
	maxBucket  float64
	refillRate float64
	lastRefill time.Time

	// RPD (Requests Per Day)
	limitRPD     int
	usedRPD      int
	lastResetRPD time.Time

	usageID string // Cached DB ID for today

	mu            sync.Mutex
	flushInterval time.Duration
	allowOverage  bool
}

func NewGeminiProvider(apiKeys []KeyConfig, dbClient db.Executor) *GeminiProvider {
	if len(apiKeys) == 0 {
		return &GeminiProvider{}
	}

	// Create a buffered channel to hold pending jobs
	// Buffer size can be adjusted, keeping it reasonable to prevent OOM but allow burst
	jobQueue := make(chan EmbedJob, 100)
	generateQueue := make(chan GenerateJob, 100)

	ctx, cancel := context.WithCancel(context.Background())

	p := &GeminiProvider{
		jobQueue:      jobQueue,
		generateQueue: generateQueue,
		dbClient:      dbClient,
		workers:       make([]*worker, 0, len(apiKeys)),
		ctx:           ctx,
		cancel:        cancel,
	}

	for _, cfg := range apiKeys {
		w := newWorker(ctx, cfg, dbClient)
		p.workers = append(p.workers, w)
		p.wg.Add(1)
		go func(w *worker) {
			defer p.wg.Done()
			w.startLoop(jobQueue, generateQueue, p.ctx)
		}(w)
	}

	return p
}

func (p *GeminiProvider) Name() string {
	return "gemini"
}

func (p *GeminiProvider) IsFunctional() bool {
	return p.jobQueue != nil
}

// Stop gracefully shuts down the AI client and waits for workers to finish
func (p *GeminiProvider) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	if p.jobQueue != nil {
		close(p.jobQueue)
		p.jobQueue = nil
	}
	if p.generateQueue != nil {
		close(p.generateQueue)
		p.generateQueue = nil
	}
	p.wg.Wait()
}

// EmbedText generates a vector embedding for the given text
func (p *GeminiProvider) EmbedText(ctx context.Context, text string) ([]float32, error) {
	res, err := p.BatchEmbedText(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return res[0], nil
}

// BatchEmbedText submits a batch of texts to the worker pool and waits for the result
func (p *GeminiProvider) BatchEmbedText(ctx context.Context, texts []string) ([][]float32, error) {
	if p.jobQueue == nil {
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
	select {
	case p.jobQueue <- job:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.ctx.Done():
		return nil, p.ctx.Err()
	}

	// Wait for result
	select {
	case result := <-resultChan:
		return result.Embeddings, result.Error
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.ctx.Done():
		return nil, p.ctx.Err()
	}
}

// GenerateContent generates text content using the AI model
func (p *GeminiProvider) GenerateContent(ctx context.Context, contents []Content, config GenerationConfig) (Candidate, error) {
	if p.generateQueue == nil {
		return Candidate{}, fmt.Errorf("AI client not initialized or no keys available")
	}

	resultChan := make(chan GenerateResult, 1)
	job := GenerateJob{
		Contents:   contents,
		Config:     config,
		ResultChan: resultChan,
	}

	select {
	case p.generateQueue <- job:
	case <-ctx.Done():
		return Candidate{}, ctx.Err()
	case <-p.ctx.Done():
		return Candidate{}, p.ctx.Err()
	}

	select {
	case result := <-resultChan:
		return result.Response, result.Error
	case <-ctx.Done():
		return Candidate{}, ctx.Err()
	case <-p.ctx.Done():
		return Candidate{}, p.ctx.Err()
	}
}

// --- Async Batch API Methods ---

// CreateBatchEmbedJob initiates an asynchronous batch embedding process via the Gemini v1beta API.
func (p *GeminiProvider) CreateBatchEmbedJob(ctx context.Context, texts []string) (string, error) {
	if len(p.workers) == 0 {
		return "", fmt.Errorf("no active workers")
	}
	// Use the first worker for now (assuming all keys are valid for batch ops)
	// Ideally we should load balance or use a specific key for batch operations
	w := p.workers[0]
	return w.createBatchEmbedJob(ctx, texts)
}

// CreateBatchGenerateJob submits an asynchronous batch content generation job
func (p *GeminiProvider) CreateBatchGenerateJob(ctx context.Context, model string, contents [][]Content, config GenerationConfig) (string, error) {
	if len(p.workers) == 0 {
		return "", fmt.Errorf("no active workers")
	}
	w := p.workers[0]
	return w.createBatchGenerateJob(ctx, model, contents, config)
}

// GetBatchJob retrieves the status of a batch job
func (p *GeminiProvider) GetBatchJob(ctx context.Context, name string) (*BatchJobStatus, error) {
	if len(p.workers) == 0 {
		return nil, fmt.Errorf("no active workers")
	}
	w := p.workers[0]
	return w.getBatchJob(ctx, name)
}

// GetBatchResults retrieves the results of a completed batch job
// Since the output is likely a file URI or inline responses, we need to handle that.
// The current implementation assumes inline responses or simple file reading if applicable.
// However, based on API docs, it might return a file URI.
func (p *GeminiProvider) GetBatchResults(ctx context.Context, outputURI string) ([][]float32, error) {
	// Not implemented fully as it depends on whether we get a file URI or inline response.
	// For inline responses in the batch status, we extract them there.
	// If the output is a file, we need a separate method to download/read it.
	// For now, let's assume we rely on what GetBatchJob returns if it includes inline results.
	// If `responses` are in the output object of the job status, we are good.
	// If `responsesFile` is set, we need to download it.

	// This function is a placeholder for downloading the file if needed.
	// Since `GetBatchJob` returns the metadata, the caller usually decides what to do.
	return nil, fmt.Errorf("not implemented: use GetBatchJob and check for inline responses or file URI")
}

// --- Worker Implementation ---

func newWorker(ctx context.Context, cfg KeyConfig, dbClient db.Executor) *worker {
	// Apply Safety Factor (0.9) to prevent edge-case overages
	safeRPM := int(float64(cfg.RPM) * 0.9)
	safeTPM := int(float64(cfg.TPM) * 0.9)
	if safeRPM < 1 && cfg.RPM > 0 {
		safeRPM = 1
	}

	// Calculate how much time each request costs based on RPM.
	var costInterval time.Duration
	if safeRPM <= 0 {
		costInterval = 0 // No limit
	} else {
		costInterval = time.Minute / time.Duration(safeRPM)
	}

	// TPM Token Bucket Init
	refillRate := float64(safeTPM) / 60.0
	maxBucket := float64(safeTPM)

	w := &worker{
		apiKey:        cfg.Key,
		owner:         cfg.Owner,
		client:        &http.Client{Timeout: 30 * time.Second},
		dbClient:      dbClient,
		costInterval:  costInterval,
		limitTPM:      safeTPM,
		limitRPD:      cfg.RPD,
		lastResetTPM:  time.Now(),
		lastResetRPD:  time.Now(),
		nextAvailable: time.Now(),

		tpmBucket:     maxBucket,
		maxBucket:     maxBucket,
		refillRate:    refillRate,
		lastRefill:    time.Now(),
		flushInterval: 5 * time.Second,
		allowOverage:  cfg.AllowOverage,
	}

	if cfg.FlushInterval > 0 {
		w.flushInterval = cfg.FlushInterval
	}

	w.updateUsageID()
	w.loadUsageFromDB(ctx)

	return w
}

func (w *worker) startLoop(embedQueue <-chan EmbedJob, genQueue <-chan GenerateJob, ctx context.Context) {
	logger.Info("AI Worker started for key ...%s", w.shortKey())

	go w.flushLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-embedQueue:
			if !ok {
				return
			}
			w.processJob(job, ctx)
		case job, ok := <-genQueue:
			if !ok {
				return
			}
			w.processGenerateJob(job, ctx)
		}
	}
}

func (w *worker) processJob(job EmbedJob, ctx context.Context) {
	// 1. Rate Limiting Logic
	w.waitRateLimits(job.Texts, ctx)

	// 2. Execute Request
	var embeddings [][]float32
	var err error
	maxRetries := 3

	for i := 0; i <= maxRetries; i++ {
		// Check context before request
		if ctx.Err() != nil {
			err = ctx.Err()
			break
		}

		var retryDelay time.Duration
		embeddings, retryDelay, err = w.doEmbed(job.Texts)

		if err == nil {
			break
		}

		if retryDelay > 0 {
			// Rate limit hit
			logger.Warn("AI Worker (...%s) rate limited. Waiting %v...", w.shortKey(), retryDelay)
			if sleepErr := sleepContext(ctx, retryDelay); sleepErr != nil {
				err = sleepErr
				break
			}
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

func (w *worker) processGenerateJob(job GenerateJob, ctx context.Context) {
	// 1. Estimate Token Cost
	texts := []string{}
	for _, c := range job.Contents {
		for _, p := range c.Parts {
			texts = append(texts, p.Text)
		}
	}
	w.waitRateLimits(texts, ctx)

	// 2. Execute Request
	var candidate Candidate
	var err error
	maxRetries := 3

	for i := 0; i <= maxRetries; i++ {
		// Check context before request
		if ctx.Err() != nil {
			err = ctx.Err()
			break
		}

		var retryDelay time.Duration
		candidate, retryDelay, err = w.doGenerate(job.Contents, job.Config)

		if err == nil {
			break
		}

		if retryDelay > 0 {
			// Rate limit hit
			logger.Warn("AI Worker (...%s) rate limited. Waiting %v...", w.shortKey(), retryDelay)
			if sleepErr := sleepContext(ctx, retryDelay); sleepErr != nil {
				err = sleepErr
				break
			}
			continue
		}

		// Non-retriable error
		break
	}

	// 3. Send Result
	job.ResultChan <- GenerateResult{
		Response: candidate,
		Error:    err,
	}
}

func (w *worker) waitRateLimits(texts []string, ctx context.Context) {
	estimatedTokens := 0
	for _, t := range texts {
		estimatedTokens += len(t) / 3 // More conservative estimation (was / 4)
		if len(t) > 0 {
			estimatedTokens++
		}
	}

	now := time.Now()

	// Reset Counters if needed
	w.mu.Lock()
	if now.Format("2006-01-02") != w.lastResetRPD.Format("2006-01-02") {
		// Flush final usage for the previous day
		w.updateDBUsage(ctx, w.usageID, w.usedRPD, w.owner)

		w.usedRPD = 0
		w.lastResetRPD = now
		w.updateUsageID()
	}
	// Store these values to check constraints without holding lock during sleep
	limitRPD := w.limitRPD
	usedRPD := w.usedRPD
	w.mu.Unlock()

	// Check RPD (Hard Stop) - Bypass if overage allowed
	if !w.allowOverage && limitRPD > 0 && usedRPD+1 > limitRPD {
		// This worker is done for the day.
		logger.Warn("AI Worker (...%s) exhausted RPD (%d). Pausing...", w.shortKey(), limitRPD)
		for {
			if err := sleepContext(ctx, 10*time.Minute); err != nil {
				return
			}
			now = time.Now()

			w.mu.Lock()
			// Check if day changed while sleeping
			if now.Format("2006-01-02") != w.lastResetRPD.Format("2006-01-02") {
				// New Day!
				// Flush final usage for the previous day
				w.updateDBUsage(ctx, w.usageID, w.usedRPD, w.owner)

				w.usedRPD = 0
				w.lastResetRPD = now
				w.updateUsageID()
				w.mu.Unlock()
				break
			}
			w.mu.Unlock()
		}
	}

	// Check TPM (Token Bucket)
	if w.limitTPM > 0 {
		// Refill
		now = time.Now()
		elapsed := now.Sub(w.lastRefill).Seconds()
		w.tpmBucket += elapsed * w.refillRate
		if w.tpmBucket > w.maxBucket {
			w.tpmBucket = w.maxBucket
		}
		w.lastRefill = now

		cost := float64(estimatedTokens)
		if w.tpmBucket >= cost {
			w.tpmBucket -= cost
		} else {
			// Need to wait
			needed := cost - w.tpmBucket
			if w.refillRate > 0 {
				waitTimeSeconds := needed / w.refillRate
				wait := time.Duration(waitTimeSeconds * float64(time.Second))

				if wait > 5*time.Second {
					logger.Debug("AI: Rate limit throttling ...%s. Waiting %v (Cost: %d)", w.shortKey(), wait, estimatedTokens)
				}

				if err := sleepContext(ctx, wait); err != nil {
					return
				}

				// Reset bucket to 0 (consumed what we waited for)
				w.tpmBucket = 0
				w.lastRefill = time.Now()
			}
		}
	}

	// Check RPM (Interval)
	now = time.Now()
	if now.Before(w.nextAvailable) {
		if err := sleepContext(ctx, w.nextAvailable.Sub(now)); err != nil {
			return
		}
	}

	// Update State
	w.nextAvailable = time.Now().Add(w.costInterval)

	w.mu.Lock()
	w.usedRPD++
	w.mu.Unlock()
}

func sleepContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (w *worker) flushLoop(ctx context.Context) {
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	// Track last flushed values locally to avoid holding lock unnecessarily
	var lastFlushedRPD int = -1
	var lastFlushedID string = ""

	// Initial sync (optional, or wait for first tick)
	w.mu.Lock()
	lastFlushedRPD = w.usedRPD
	lastFlushedID = w.usageID
	w.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			// Final flush
			w.mu.Lock()
			currRPD := w.usedRPD
			currID := w.usageID
			w.mu.Unlock()
			if currRPD != lastFlushedRPD || currID != lastFlushedID {
				// Flush final usage
				w.updateDBUsage(ctx, currID, currRPD, w.owner)
			}
			return

		case <-ticker.C:
			w.mu.Lock()
			currRPD := w.usedRPD
			currID := w.usageID
			w.mu.Unlock()

			if currRPD != lastFlushedRPD || currID != lastFlushedID {
				w.updateDBUsage(ctx, currID, currRPD, w.owner)
				lastFlushedRPD = currRPD
				lastFlushedID = currID
			}
		}
	}
}

func (w *worker) updateUsageID() {
	// ID: key_usage:<hash>_<date>
	h := sha256.New()
	h.Write([]byte(w.apiKey))
	hash := hex.EncodeToString(h.Sum(nil))[:8] // Short hash
	date := time.Now().Format("20060102")
	w.usageID = fmt.Sprintf("%s:%s_%s", schema.TableKeyUsage, hash, date)
}

func (w *worker) loadUsageFromDB(ctx context.Context) {
	if w.dbClient == nil {
		return
	}
	// Select requests
	ql := fmt.Sprintf("SELECT requests FROM %s;", w.usageID)
	res, err := w.dbClient.Execute(ctx, ql)
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

func (w *worker) updateDBUsage(ctx context.Context, id string, count int, owner string) {
	if w.dbClient == nil {
		return
	}

	// Update
	updateQL := fmt.Sprintf("UPDATE %s SET requests = $req, last_updated = time::now();", id)
	vars := map[string]interface{}{
		"req": count,
	}

	_, err := w.dbClient.SmartQuery(ctx, updateQL, vars)
	if err != nil {
		// If update failed (likely doesn't exist), try CREATE
		createQL := fmt.Sprintf("CREATE %s SET requests = $req, owner = $owner, date = time::now();", id)
		createVars := map[string]interface{}{
			"req":   count,
			"owner": owner,
		}
		w.dbClient.SmartQuery(ctx, createQL, createVars)
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

// doGenerate performs the actual HTTP request. Returns (result, retryDelay, error).
func (w *worker) doGenerate(contents []Content, config GenerationConfig) (Candidate, time.Duration, error) {
	logger.Debug("AI: Worker ...%s processing generation", w.shortKey())

	// Try PreferredGenerationModel first, then GenerationModel
	models := []string{PreferredGenerationModel, GenerationModel}
	var lastErr error

	for _, model := range models {
		url := fmt.Sprintf("%s/%s:generateContent?key=%s", BaseURL, model, w.apiKey)

		payload := GenerateContentRequest{
			Contents:         contents,
			GenerationConfig: config,
		}
		jsonBody, err := json.Marshal(payload)
		if err != nil {
			return Candidate{}, 0, err
		}

		start := time.Now()
		resp, err := w.client.Post(url, "application/json", bytes.NewBuffer(jsonBody))
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()
		duration := time.Since(start)

		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode == 200 {
			logger.Debug("AI: Worker ...%s generation success with %s (%v)", w.shortKey(), model, duration)
			var result GenerateContentResponse
			if err := json.Unmarshal(body, &result); err != nil {
				return Candidate{}, 0, fmt.Errorf("parsing error: %w", err)
			}
			if len(result.Candidates) == 0 {
				return Candidate{}, 0, fmt.Errorf("no candidates returned")
			}
			cand := result.Candidates[0]
			cand.UsageMetadata = result.UsageMetadata
			return cand, 0, nil
		}

		if resp.StatusCode == 429 {
			retryDelay := parseRetryDelay(body)
			if retryDelay == 0 {
				retryDelay = 5 * time.Second
			}
			return Candidate{}, retryDelay, fmt.Errorf("rate limit exceeded")
		}

		// If 404, we try the next model
		if resp.StatusCode == 404 {
			logger.Warn("AI: Model %s not found, trying next...", model)
			lastErr = fmt.Errorf("gemini api error %d: %s", resp.StatusCode, string(body))
			continue
		}

		return Candidate{}, 0, fmt.Errorf("gemini api error %d: %s", resp.StatusCode, string(body))
	}

	return Candidate{}, 0, lastErr
}

// --- Worker Async Batch Implementation ---

func (w *worker) createBatchEmbedJob(ctx context.Context, texts []string) (string, error) {
	url := fmt.Sprintf("%s/%s:asyncBatchEmbedContent?key=%s", BaseURL, EmbeddingModel, w.apiKey)

	reqItems := make([]EmbedRequestItem, len(texts))
	for i, t := range texts {
		reqItems[i] = EmbedRequestItem{
			Model: EmbeddingModel,
			Content: Content{
				Parts: []Part{{Text: t}},
			},
		}
	}

	// Use displayName to store something useful? Maybe just timestamp.
	payload := map[string]interface{}{
		"model":       EmbeddingModel,
		"displayName": fmt.Sprintf("batch_%d", time.Now().UnixNano()),
		"inputConfig": map[string]interface{}{
			"requests": map[string]interface{}{
				"requests": reqItems,
			},
		},
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 200 {
		// Response is an Operation object
		var op Operation
		if err := json.Unmarshal(body, &op); err != nil {
			return "", fmt.Errorf("parsing operation error: %w", err)
		}
		return op.Name, nil
	}

	return "", fmt.Errorf("gemini async batch error %d: %s", resp.StatusCode, string(body))
}

func (w *worker) getBatchJob(ctx context.Context, name string) (*BatchJobStatus, error) {
	// name is like "batches/12345" or "operations/..." ?
	// API Docs say: POST .../asyncBatchEmbedContent returns Operation.
	// We need to poll Operation or Batches?
	// The response from asyncBatchEmbedContent is an Operation.
	// Usually Operation.name can be polled via /v1beta/{name}.
	// If it's a Batch resource, we might need batches.get.
	// Let's assume the name returned is the resource to poll.

	url := fmt.Sprintf("%s/%s?key=%s", BaseURL, name, w.apiKey)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("get batch error %d: %s", resp.StatusCode, string(body))
	}

	// It could return an Operation or a Batch resource depending on what we polled.
	// If it's an Operation, we check `done` and `response`.
	// If it's a Batch, we check `state`.
	// For simplicity, let's map it to our internal status struct.

	// Try parsing as Operation first
	var op Operation
	if err := json.Unmarshal(body, &op); err == nil && op.Name != "" {
		status := &BatchJobStatus{
			Name: op.Name,
			Done: op.Done,
		}
		if op.Error != nil {
			status.Error = fmt.Errorf("operation error: %s", op.Error.Message)
		}
		// If done, usually response contains the result?
		// For asyncBatchEmbedContent, the result is likely a BatchEmbedResponse or similar.
		if op.Done && op.Response != nil {
			// Extract embeddings
			// The response field is a map[string]interface{}.
			// We need to marshal/unmarshal or map it.
			// However, looking at docs, Batch API might return a separate Batch resource?
			// "Enqueues a batch ... If successful, the response body contains an instance of Operation."

			// Let's try to parse the response part as BatchEmbedResponse
			// Note: "The normal, successful response of the operation... For other methods, the response should have the type XxxResponse"
			// So it should be BatchEmbedResponse.
			// BUT, the `response` field in Operation is `map[string]interface{}` (Any).

			if embeddingsRaw, ok := op.Response["embeddings"]; ok {
				// Manually extract
				jsonBytes, _ := json.Marshal(map[string]interface{}{"embeddings": embeddingsRaw})
				var ber BatchEmbedResponse
				if err := json.Unmarshal(jsonBytes, &ber); err == nil {
					status.Embeddings = make([][]float32, len(ber.Embeddings))
					for i, e := range ber.Embeddings {
						status.Embeddings[i] = e.Values
					}
				}
			}

			if candidatesRaw, ok := op.Response["candidates"]; ok {
				jsonBytes, _ := json.Marshal(map[string]interface{}{"candidates": candidatesRaw})
				var bgr BatchGenerateResponse
				if err := json.Unmarshal(jsonBytes, &bgr); err == nil {
					status.Candidates = bgr.Candidates
				}
			}
		}
		return status, nil
	}

	return nil, fmt.Errorf("unknown response format")
}

// --- DTOs ---

type EmbeddingRequest struct {
	Model   string  `json:"model"`
	Content Content `json:"content"`
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

type GenerateContentRequest struct {
	Contents         []Content        `json:"contents"`
	GenerationConfig GenerationConfig `json:"generationConfig,omitempty"`
}



type GenerateContentResponse struct {
	Candidates    []Candidate    `json:"candidates"`
	UsageMetadata *UsageMetadata `json:"usageMetadata,omitempty"`
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

// Operation resource from Google API
type Operation struct {
	Name     string                 `json:"name"`
	Metadata map[string]interface{} `json:"metadata"`
	Done     bool                   `json:"done"`
	Error    *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Response map[string]interface{} `json:"response,omitempty"`
}



type BatchGenerateRequest struct {
	Requests []GenerateContentRequest `json:"requests"`
}

type BatchGenerateResponse struct {
	Candidates []Candidate `json:"candidates"`
}

func (w *worker) createBatchGenerateJob(ctx context.Context, model string, contents [][]Content, config GenerationConfig) (string, error) {
	if model == "" {
		model = PreferredGenerationModel
	}
	url := fmt.Sprintf("%s/%s:batchGenerateContent?key=%s", BaseURL, model, w.apiKey)

	reqItems := make([]GenerateContentRequest, len(contents))
	for i, c := range contents {
		reqItems[i] = GenerateContentRequest{
			Contents:         c,
			GenerationConfig: config,
		}
	}

	payload := map[string]interface{}{
		"model":       model,
		"displayName": fmt.Sprintf("gen_batch_%d", time.Now().UnixNano()),
		"inputConfig": map[string]interface{}{
			"requests": map[string]interface{}{
				"requests": reqItems,
			},
		},
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 200 {
		var op Operation
		if err := json.Unmarshal(body, &op); err != nil {
			return "", fmt.Errorf("parsing operation error: %w", err)
		}
		return op.Name, nil
	}

	return "", fmt.Errorf("gemini async batch gen error %d: %s", resp.StatusCode, string(body))
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
