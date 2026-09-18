package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/terenzif/ibis-assistant/internal/db"
	"github.com/terenzif/ibis-assistant/internal/logger"
	"github.com/terenzif/ibis-assistant/internal/schema"
)

const (
	BatchSize    = 100 // Adjust based on API limits.
	PollInterval = 10 * time.Second
)

type BatchManager struct {
	DB            db.Executor
	AI            *Client // Use concrete client to access batch methods
	wg            sync.WaitGroup
	ctx           context.Context
	cancel        context.CancelFunc
	DiscoveryRoot string
	MaxDeltaSize  int
}

func NewBatchManager(dbClient db.Executor, aiClient *Client, discoveryRoot string, maxDeltaSize int) *BatchManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &BatchManager{
		DB:            dbClient,
		AI:            aiClient,
		ctx:           ctx,
		cancel:        cancel,
		DiscoveryRoot: discoveryRoot,
		MaxDeltaSize:  maxDeltaSize,
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
				bm.processPendingCommits() // Extract patches and create chunks
				bm.processPendingChunks()  // Embed all pending chunks (file & commit)
			}
		}
	}
}

// processPendingChunks finds chunks waiting for embedding and submits them in batches
func (bm *BatchManager) processPendingChunks() {
	if bm.DB == nil || bm.AI == nil || !bm.AI.IsEmbeddingFunctional() {
		return
	}

	// Separate queries to avoid issues with multi-table SELECT syntax in some SurrealDB versions
	tables := []string{schema.TableFileChunk, schema.TableCommitChunk}
	var allRows []interface{}

	for _, table := range tables {
		ql := fmt.Sprintf("SELECT id, content FROM %s WHERE batch_status = 'pending' LIMIT %d;", table, BatchSize)
		res, err := bm.DB.Execute(bm.ctx, ql)
		if err != nil {
			logger.Error("BatchManager: Failed to fetch pending chunks from %s: %v", table, err)
			continue
		}
		if res != nil {
			logger.Debug("BatchManager: Query for %s returned type %T", table, res)
		}
		if rows, ok := res.([]interface{}); ok {
			allRows = append(allRows, rows...)
		} else if res != nil {
			logger.Debug("BatchManager: Failed to cast result to []interface{}. Res type=%T", res)
		}
		if len(allRows) >= BatchSize {
			allRows = allRows[:BatchSize]
			break
		}
	}

	if len(allRows) == 0 {
		return // Nothing to do
	}

	var chunkIDs []string
	var texts []string
	skipped := 0

	for _, r := range allRows {
		row, ok := r.(map[string]interface{})
		if !ok {
			skipped++
			continue
		}

		id := db.CoerceRecordID(row["id"])
		content, _ := row["content"].(string)

		if id != "" && content != "" {
			chunkIDs = append(chunkIDs, id)
			texts = append(texts, content)
		} else {
			skipped++
		}
	}

	if len(chunkIDs) == 0 {
		logger.Warn("BatchManager: %d pending rows but 0 usable chunk IDs (skipped=%d)", len(allRows), skipped)
		return
	}

	logger.Info("BatchManager: Found %d chunks. Embedding synchronously...", len(chunkIDs))

	// 3. Submit to AI synchronously using the worker pool
	embeddings, err := bm.AI.BatchEmbedText(bm.ctx, texts)
	if err != nil {
		logger.Error("BatchManager: Failed to embed batch: %v", err)
		return
	}

	// 4. Update Chunks with results in a transaction
	var sb strings.Builder
	sb.WriteString("BEGIN TRANSACTION; ")

	for i, id := range chunkIDs {
		vecJson, _ := json.Marshal(embeddings[i])
		safeID := db.FormatRecordID("", id)
		sb.WriteString(fmt.Sprintf("UPDATE %s SET embedding = %s, batch_status = 'completed'; ",
			safeID, string(vecJson)))
	}

	sb.WriteString("COMMIT TRANSACTION;")

	if _, err := bm.DB.Execute(bm.ctx, sb.String()); err != nil {
		logger.Error("BatchManager: Failed to save batch results: %v", err)
	} else {
		logger.Info("BatchManager: Successfully saved %d embeddings", len(chunkIDs))
	}
}

// processPendingCommits finds pending commits, extracts patches via Git, chunks them, and creates TableCommitChunk
func (bm *BatchManager) processPendingCommits() {
	if bm.DB == nil {
		return
	}

	ql := fmt.Sprintf(`SELECT id, hash, repo.name as repo_name, message 
		FROM %s WHERE batch_status = 'pending' LIMIT %d;`, schema.TableCommit, BatchSize)
	res, err := bm.DB.Execute(bm.ctx, ql)
	if err != nil {
		logger.Error("BatchManager: Failed to fetch pending commits: %v", err)
		return
	}

	rows, ok := res.([]interface{})
	if !ok || len(rows) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString("BEGIN TRANSACTION;\n")
	hasUpdates := false

	for _, r := range rows {
		row, ok := r.(map[string]interface{})
		if !ok {
			continue
		}

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

		hash, _ := row["hash"].(string)
		repoName, _ := row["repo_name"].(string)
		message, _ := row["message"].(string)

		if id == "" || hash == "" || repoName == "" {
			continue
		}

		absRepoPath := filepath.Join(bm.DiscoveryRoot, repoName)

		cmd := exec.CommandContext(bm.ctx, "git", "diff", hash+"^", hash)
		cmd.Dir = absRepoPath
		out, err := cmd.Output()

		var patch string
		if err != nil {
			cmdShow := exec.CommandContext(bm.ctx, "git", "show", "--format=", "--patch", hash)
			cmdShow.Dir = absRepoPath
			if outShow, errShow := cmdShow.Output(); errShow == nil {
				patch = string(outShow)
			}
		} else {
			patch = string(out)
		}

		if patch == "" && message == "" {
			safeID := db.FormatRecordID("", id)
			sb.WriteString(fmt.Sprintf("UPDATE %s SET batch_status = 'completed';\n", safeID))
			hasUpdates = true
			continue
		}

		if bm.MaxDeltaSize <= 0 {
			bm.MaxDeltaSize = 8000
		}

		fullText := fmt.Sprintf("COMMIT MESSAGE:\n%s\n\nPATCH:\n%s", message, patch)
		chunks := chunkString(fullText, bm.MaxDeltaSize)

		for i, chunkText := range chunks {
			chunkID := db.FormatRecordID(schema.TableCommitChunk, fmt.Sprintf("%s_chunk%d", hash, i))
			escapedText := db.EscapeSQL(chunkText)
			sb.WriteString(fmt.Sprintf("UPDATE %s SET content = '%s', batch_status = 'pending', commit_hash = '%s';\n",
				chunkID, escapedText, hash))

			safeCommitID := db.FormatRecordID("", id)
			sb.WriteString(fmt.Sprintf("RELATE %s->%s->%s;\n", safeCommitID, schema.EdgeHasCommitChunk, chunkID))
		}

		safeID := db.FormatRecordID("", id)
		sb.WriteString(fmt.Sprintf("UPDATE %s SET batch_status = 'completed';\n", safeID))
		hasUpdates = true
	}

	sb.WriteString("COMMIT TRANSACTION;\n")

	if hasUpdates {
		if _, err := bm.DB.Execute(bm.ctx, sb.String()); err != nil {
			logger.Error("BatchManager: Failed to save commit chunks: %v", err)
		} else {
			logger.Info("BatchManager: Processed %d pending commits into chunks.", len(rows))
		}
	}
}

func chunkString(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	lines := strings.Split(text, "\n")
	var current string

	for _, line := range lines {
		if len(current)+len(line)+1 > maxLen && len(current) > 0 {
			chunks = append(chunks, current)
			current = line
		} else {
			if len(current) > 0 {
				current += "\n" + line
			} else {
				current = line
			}
		}
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}
