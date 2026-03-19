package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

const (
	BatchSize    = 100 // Adjust based on API limits. Google usually allows ~100-1000 requests per batch.
	PollInterval = 10 * time.Second
)

type BatchManager struct {
	DB     db.Executor
	AI     *Client // Use concrete client to access batch methods
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func NewBatchManager(dbClient db.Executor, aiClient *Client) *BatchManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &BatchManager{
		DB:     dbClient,
		AI:     aiClient,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (bm *BatchManager) Start() {
	bm.wg.Add(1)
	go func() {
		defer bm.wg.Done()
		bm.runLoop()
	}()
}

func (bm *BatchManager) Stop() {
	bm.cancel()
	bm.wg.Wait()
}

func (bm *BatchManager) runLoop() {
	logger.Info("BatchManager: Started background loop")
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bm.ctx.Done():
			logger.Info("BatchManager: Stopping...")
			return
		case <-ticker.C:
			if bm.DB != nil {
				bm.processPendingChunks()
				bm.checkActiveBatches()
			}
		}
	}
}

// processPendingChunks finds chunks waiting for embedding and submits them in batches
func (bm *BatchManager) processPendingChunks() {
	if bm.DB == nil {
		return
	}
	// 1. Find pending chunks
	// To avoid race conditions in a scaled env (though this is single instance),
	// we should mark them as 'processing' or similar.
	// For now, we select limit N.

	ql := fmt.Sprintf("SELECT id, content FROM %s WHERE batch_status = 'pending' LIMIT %d;", schema.TableFileChunk, BatchSize)
	res, err := bm.DB.Execute(ql)
	if err != nil {
		logger.Error("BatchManager: Failed to fetch pending chunks: %v", err)
		return
	}

	rows, ok := res.([]interface{})
	if !ok || len(rows) == 0 {
		return // Nothing to do
	}

	logger.Info("BatchManager: Found %d pending chunks", len(rows))

	var chunkIDs []string
	var texts []string

	for _, r := range rows {
		row, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		
		// Robust extraction of ID and content.
		// In SurrealDB v3, IDs might be represented as RecordID strings or objects.
		var id string
		if rawID, exists := row["id"]; exists {
			switch v := rawID.(type) {
			case string:
				id = v
			case map[string]interface{}:
				if idVal, ok := v["id"].(string); ok {
					id = idVal
				}
			}
		}
		
		content, _ := row["content"].(string)

		if id != "" && content != "" {
			chunkIDs = append(chunkIDs, id)
			texts = append(texts, content)
		}
	}

	if len(chunkIDs) == 0 {
		logger.Debug("BatchManager: No valid chunks found in %d candidate rows", len(rows))
		return
	}

	// 2. Mark chunks as 'processing' to prevent re-selection immediately?
	// Actually, if we submit successfully, we update them. If we fail, they stay pending.
	// But if submission takes time, next tick might pick them up.
	// Best practice: Update to 'queued' first.
	// Let's rely on the fact that we process them quickly here.

	// 3. Submit to AI
	jobName, err := bm.AI.CreateBatchEmbedJob(bm.ctx, texts)
	if err != nil {
		logger.Error("BatchManager: Failed to create batch job: %v", err)
		return
	}

	logger.Info("BatchManager: Submitted Job %s with %d items", jobName, len(texts))

	// 4. Create Batch Job Record
	// Store mapping of order -> chunkID if needed, or just link chunks to batchID.
	// Since order is preserved, we can just query chunks by batch_id later?
	// No, easier to store the list of IDs in the batch job record or rely on `chunk.batch_id`.
	// We update chunks to point to this batch.

	// Transaction to create job and update chunks
	var sb strings.Builder
	sb.WriteString("BEGIN TRANSACTION; ")

	// Create Job
	// We need to store the chunk IDs in order to map results back correctly!
	// SurrealDB allows array of strings.
	chunkIDsJson, _ := json.Marshal(chunkIDs)

	// jobID: batch_job:<jobName_sanitized>
	jobID := fmt.Sprintf("%s:%s", schema.TableBatchJob, db.SanitizeID(jobName))

	sb.WriteString(fmt.Sprintf("CREATE %s SET name = '%s', status = 'submitted', chunk_ids = %s, created_at = time::now(); ",
		jobID, jobName, string(chunkIDsJson)))

	// Update Chunks
	// We can use WHERE id IN [...]
	// Ensure IDs are properly quoted if they contain special characters.
	quotedIDs := make([]string, len(chunkIDs))
	for i, id := range chunkIDs {
		quotedIDs[i] = fmt.Sprintf("'%s'", db.EscapeSQL(id))
	}
	idList := "[" + strings.Join(quotedIDs, ", ") + "]"
	sb.WriteString(fmt.Sprintf("UPDATE %s SET batch_id = '%s', batch_status = 'submitted' WHERE id IN %s; ",
		schema.TableFileChunk, jobID, idList))

	sb.WriteString("COMMIT TRANSACTION;")

	if _, err := bm.DB.Execute(sb.String()); err != nil {
		logger.Error("BatchManager: Failed to save batch job info: %v", err)
	}
}

// checkActiveBatches polls submitted jobs for completion
func (bm *BatchManager) checkActiveBatches() {
	// 1. Find submitted jobs
	ql := fmt.Sprintf("SELECT id, name, chunk_ids FROM %s WHERE status = 'submitted';", schema.TableBatchJob)
	res, err := bm.DB.Execute(ql)
	if err != nil {
		logger.Error("BatchManager: Failed to fetch active batches: %v", err)
		return
	}

	rows, ok := res.([]interface{})
	if !ok || len(rows) == 0 {
		return
	}

	for _, r := range rows {
		row, ok := r.(map[string]interface{})
		if !ok {
			continue
		}

		dbID, _ := row["id"].(string)
		jobName, _ := row["name"].(string)

		// Parse chunk_ids safely
		var chunkIDs []string
		if cids, ok := row["chunk_ids"].([]interface{}); ok {
			for _, c := range cids {
				if s, ok := c.(string); ok {
					chunkIDs = append(chunkIDs, s)
				}
			}
		}

		// 2. Poll API
		status, err := bm.AI.GetBatchJob(bm.ctx, jobName)
		if err != nil {
			logger.Warn("BatchManager: Error polling job %s: %v", jobName, err)
			continue
		}

		if status.Done {
			if status.Error != nil {
				logger.Error("BatchManager: Job %s failed: %v", jobName, status.Error)
				// Mark as failed, reset chunks to pending?
				bm.markJobFailed(dbID, chunkIDs, status.Error.Error())
			} else {
				// Success
				logger.Info("BatchManager: Job %s completed. Processing results...", jobName)
				bm.processJobResults(dbID, chunkIDs, status.Embeddings)
			}
		}
	}
}

func (bm *BatchManager) processJobResults(jobID string, chunkIDs []string, embeddings [][]float32) {
	if len(chunkIDs) != len(embeddings) {
		logger.Error("BatchManager: Mismatch in result count for %s. Chunks: %d, Embeddings: %d", jobID, len(chunkIDs), len(embeddings))
		bm.markJobFailed(jobID, chunkIDs, "Count mismatch")
		return
	}

	var sb strings.Builder
	sb.WriteString("BEGIN TRANSACTION; ")

	// Update chunks with embeddings
	for i, id := range chunkIDs {
		vecJson, _ := json.Marshal(embeddings[i])
		// batch_status = 'completed'
		sb.WriteString(fmt.Sprintf("UPDATE %s SET embedding = %s, batch_status = 'completed' WHERE id = %s; ",
			id, string(vecJson), id))
	}

	// Update Job Status
	sb.WriteString(fmt.Sprintf("UPDATE %s SET status = 'completed', completed_at = time::now(); ", jobID))
	sb.WriteString("COMMIT TRANSACTION;")

	if _, err := bm.DB.Execute(sb.String()); err != nil {
		logger.Error("BatchManager: Failed to apply results for %s: %v", jobID, err)
	} else {
		logger.Info("BatchManager: Successfully updated %d chunks from job %s", len(chunkIDs), jobID)
	}
}

func (bm *BatchManager) markJobFailed(jobID string, chunkIDs []string, reason string) {
	var sb strings.Builder
	sb.WriteString("BEGIN TRANSACTION; ")

	// Reset chunks to 'pending' so they are picked up again (retry logic could be smarter)
	// Maybe add retry count?
	quotedIDs := make([]string, len(chunkIDs))
	for i, id := range chunkIDs {
		quotedIDs[i] = fmt.Sprintf("'%s'", db.EscapeSQL(id))
	}
	idList := "[" + strings.Join(quotedIDs, ", ") + "]"
	sb.WriteString(fmt.Sprintf("UPDATE %s SET batch_status = 'pending', batch_error = '%s' WHERE id IN %s; ",
		schema.TableFileChunk, db.EscapeSQL(reason), idList))

	// Mark Job as failed
	sb.WriteString(fmt.Sprintf("UPDATE %s SET status = 'failed', error = '%s' WHERE id = %s; ",
		jobID, db.EscapeSQL(reason), jobID))

	sb.WriteString("COMMIT TRANSACTION;")

	bm.DB.Execute(sb.String())
}
