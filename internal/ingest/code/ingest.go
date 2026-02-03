package code

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/deckonline/knowledge_mcp/internal/config"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

type AIClient interface {
	BatchEmbedText(texts []string) ([][]float32, error)
	IsFunctional() bool
}

// IngestCodebase scans the repo and updates embeddings for changed files
func IngestCodebase(ctx context.Context, dbClient db.Executor, aiClient AIClient, repoPath string, cfg *config.Config) error {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return err
	}

	logger.Info("Starting code analysis for: %s", absPath)

	if aiClient == nil || !aiClient.IsFunctional() {
		return fmt.Errorf("AI vectorization is disabled: no Gemini API keys provided (set GEMINI_API_KEY or gemini_keys in config.json)")
	}

	// Prepare lookup maps from config
	supportedExts := make(map[string]bool)
	if cfg != nil {
		for _, ext := range cfg.SupportedExtensions {
			supportedExts[strings.ToLower(ext)] = true
		}
	}

	ignoredDirs := make(map[string]bool)
	if cfg != nil {
		for _, dir := range cfg.IgnoredDirs {
			ignoredDirs[dir] = true
		}
	}

	ignoredFiles := make(map[string]bool)
	if cfg != nil {
		for _, file := range cfg.IgnoredFiles {
			ignoredFiles[file] = true
		}
	}

	maxSize := int64(10 * 1024 * 1024) // Default 10MB
	if cfg != nil && cfg.MaxFileSize > 0 {
		maxSize = cfg.MaxFileSize
	}

	pathsChan := make(chan string, 1000)
	var wg sync.WaitGroup

	// Start Worker Pool
	// 20 workers is a reasonable default for concurrent IO + DB + AI wait
	numWorkers := 20
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case path, ok := <-pathsChan:
					if !ok {
						return
					}
					if err := processFile(ctx, dbClient, aiClient, path); err != nil {
						logger.Error("Error processing file %s: %v", path, err)
					}
				}
			}
		}()
	}

	// Walk the directory and feed the channel
	err = filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
				return filepath.SkipDir
			}
			if ignoredDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		if ignoredFiles[info.Name()] {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !supportedExts[ext] {
			return nil
		}

		if info.Size() > maxSize {
			logger.Debug("Skipping large file: %s (%d bytes)", path, info.Size())
			return nil
		}

		select {
		case pathsChan <- path:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})

	close(pathsChan) // Signal workers to finish
	wg.Wait()        // Wait for all workers

	if ctx.Err() != nil {
		logger.Info("Code analysis cancelled for %s", absPath)
		return ctx.Err()
	}

	logger.Info("Code analysis complete for %s", absPath)
	return err
}

func processFile(ctx context.Context, dbClient db.Executor, aiClient AIClient, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open error: %w", err)
	}
	defer f.Close()

	// 1. Calculate Hash
	hash, err := fileHash(f)
	if err != nil {
		return fmt.Errorf("hashing error: %w", err)
	}

	fileID := fmt.Sprintf("%s:%s", schema.TableFile, db.SanitizeID(path))

	// 2. Check if changed (using DB check)
	// Query existing hash
	ql := fmt.Sprintf("SELECT hash FROM %s;", fileID)
	res, err := dbClient.Execute(ql)
	if err == nil {
		// Parse response to see if hash matches.
		// Result is typically []interface{} where each item is map[string]interface{}
		if rows, ok := res.([]interface{}); ok && len(rows) > 0 {
			if row, ok := rows[0].(map[string]interface{}); ok {
				if existingHash, ok := row["hash"].(string); ok && existingHash == hash {
					return nil // Unchanged
				}
			}
		}
	}

	logger.Info("Processing %s...", filepath.Base(path))

	// 3. Update File Node
	// Update hash
	_, err = dbClient.Execute(fmt.Sprintf("UPDATE %s SET hash = '%s', path = '%s';", fileID, hash, db.EscapeSQL(path)))
	if err != nil {
		return fmt.Errorf("db update error: %w", err)
	}

	// Reset file pointer to beginning for chunking
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("seek error: %w", err)
	}

	// Chunk & Embed
	chunks, err := chunkContent(f, 1000) // 1000 chars ~ 250 tokens
	if err != nil {
		return fmt.Errorf("chunking error: %w", err)
	}

	// Delete old chunks
	// DELETE file_chunk WHERE file = $fileID
	if _, err := dbClient.Execute(fmt.Sprintf("DELETE %s WHERE file = %s;", schema.TableFileChunk, fileID)); err != nil {
		return fmt.Errorf("failed to delete old chunks: %w", err)
	}

	// Batching Logic (Gemini limit is 100 per batch)
	// We use a smaller batch size to avoid hitting RPM limits instantly if items count as requests.
	batchSize := 10
	for i := 0; i < len(chunks); i += batchSize {
		end := i + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		batch := chunks[i:end]

		// Filter empty
		var validBatch []string
		var validIndices []int
		for k, c := range batch {
			if strings.TrimSpace(c) != "" {
				validBatch = append(validBatch, c)
				validIndices = append(validIndices, i+k)
			}
		}
		if len(validBatch) == 0 {
			continue
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		vectors, err := aiClient.BatchEmbedText(validBatch)
		if err != nil {
			logger.Error("Batch embedding error for %s: %v", path, err)
			continue
		}

		// Store Results
		for k, vec := range vectors {
			originalIndex := validIndices[k]
			chunkContentStr := validBatch[k]

			vecJson, _ := json.Marshal(vec)
			chunkID := fmt.Sprintf("%s:%s_%d", schema.TableFileChunk, db.SanitizeID(path), originalIndex)

			ql := fmt.Sprintf("UPDATE %s SET file = %s, content = '%s', embedding = %s;",
				chunkID, fileID, db.EscapeSQL(chunkContentStr), string(vecJson))

			if _, err := dbClient.Execute(ql); err != nil {
				logger.Error("Failed to update chunk %s: %v", chunkID, err)
			}
		}
		logger.Info("  - Embedded %d/%d chunks for %s", end, len(chunks), filepath.Base(path))
	}

	return nil
}

func fileHash(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func chunkContent(r io.Reader, size int) ([]string, error) {
	// Very naive chunking by lines/size. 
	// Production should use a tokenizer or smarter splitter.
	var chunks []string
	scanner := bufio.NewScanner(r)
	var currentChunk strings.Builder
	
	for scanner.Scan() {
		line := scanner.Text()
		if currentChunk.Len() + len(line) > size {
			chunks = append(chunks, currentChunk.String())
			currentChunk.Reset()
		}
		currentChunk.WriteString(line)
		currentChunk.WriteByte('\n')
	}
	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return chunks, nil
}
