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

	// Load Ignore Patterns
	ignorePatterns, err := loadIgnorePatterns(absPath)
	if err != nil {
		logger.Warn("Failed to load ignore patterns for %s: %v", absPath, err)
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
	// Default to a safer concurrency level to avoid DB overload
	numWorkers := 5
	if cfg != nil && cfg.CodeConcurrency > 0 {
		numWorkers = cfg.CodeConcurrency
	}

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
					if err := processFile(ctx, dbClient, path); err != nil {
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

		// Directory Checks
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
				return filepath.SkipDir
			}
			if ignoredDirs[info.Name()] {
				return filepath.SkipDir
			}
			if isIgnored(path, absPath, ignorePatterns, true) {
				return filepath.SkipDir
			}
			return nil
		}

		// File Checks
		if ignoredFiles[info.Name()] {
			return nil
		}
		if isIgnored(path, absPath, ignorePatterns, false) {
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

	// Prune Phase: Delete files from DB that are ignored (but NOT if just missing from disk)
	if err := pruneRepo(ctx, dbClient, absPath, cfg, ignorePatterns); err != nil {
		logger.Error("Error pruning obsolete files for %s: %v", absPath, err)
	}

	logger.Info("Code analysis complete for %s", absPath)
	return err
}

func pruneRepo(ctx context.Context, dbClient db.Executor, repoPath string, cfg *config.Config, patterns []string) error {
	logger.Info("Pruning obsolete files for %s...", repoPath)

	// Fetch all files in this repo from DB
	separator := string(os.PathSeparator)
	queryPrefix := db.EscapeSQL(repoPath + separator)
	ql := fmt.Sprintf("SELECT id, path FROM %s WHERE string::starts_with(path, '%s');", schema.TableFile, queryPrefix)

	res, err := dbClient.Execute(ql)
	if err != nil {
		return fmt.Errorf("failed to fetch files for pruning: %w", err)
	}

	rows, ok := res.([]interface{})
	if !ok {
		return nil // No results
	}

	// Config lookups
	supportedExts := make(map[string]bool)
	ignoredDirs := make(map[string]bool)
	ignoredFiles := make(map[string]bool)
	maxSize := int64(10 * 1024 * 1024)

	if cfg != nil {
		for _, ext := range cfg.SupportedExtensions {
			supportedExts[strings.ToLower(ext)] = true
		}
		for _, dir := range cfg.IgnoredDirs {
			ignoredDirs[dir] = true
		}
		for _, file := range cfg.IgnoredFiles {
			ignoredFiles[file] = true
		}
		if cfg.MaxFileSize > 0 {
			maxSize = cfg.MaxFileSize
		}
	}

	var toDelete []string

	for _, r := range rows {
		row, ok := r.(map[string]interface{})
		if !ok { continue }

		id, _ := row["id"].(string)
		path, _ := row["path"].(string)

		if id == "" || path == "" { continue }

		shouldDelete := false

		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			// FILE MISSING FROM DISK
			// Strategy: Do NOT delete from DB. Preserve history.
			// "what about files previously delete so no more into HEAD but still into git history?"
			shouldDelete = false
		} else if err == nil {
			name := info.Name()

			// Check Ignore Patterns
			if isIgnored(path, repoPath, patterns, info.IsDir()) {
				logger.Debug("Deleting %s (Ignored Pattern)", path)
				shouldDelete = true
			}
			// Check Config Exclusions
			if info.IsDir() {
				if ignoredDirs[name] {
					shouldDelete = true
				}
			} else {
				if ignoredFiles[name] {
					shouldDelete = true
				}
				if info.Size() > maxSize {
					shouldDelete = true
				}
				ext := strings.ToLower(filepath.Ext(path))
				if !supportedExts[ext] {
					logger.Debug("Deleting %s (Unsupported Ext: %s)", path, ext)
					shouldDelete = true
				}
			}

			if !shouldDelete && len(ignoredDirs) > 0 {
				rel, _ := filepath.Rel(repoPath, path)
				parts := strings.Split(rel, string(os.PathSeparator))
				for _, part := range parts {
					if ignoredDirs[part] {
						logger.Debug("Deleting %s (Ignored Dir Part: %s)", path, part)
						shouldDelete = true
						break
					}
				}
			}
		}

		if shouldDelete {
			toDelete = append(toDelete, id)
		}
	}

	if len(toDelete) == 0 {
		return nil
	}

	logger.Info("Deleting %d obsolete files...", len(toDelete))

	// Batch Delete
	batchSize := 50
	for i := 0; i < len(toDelete); i += batchSize {
		end := i + batchSize
		if end > len(toDelete) {
			end = len(toDelete)
		}
		batch := toDelete[i:end]

		// Construct [id1, id2]
		var idListBuilder strings.Builder
		idListBuilder.WriteString("[")
		for j, id := range batch {
			if j > 0 { idListBuilder.WriteString(", ") }
			idListBuilder.WriteString(id)
		}
		idListBuilder.WriteString("]")
		idList := idListBuilder.String()

		// 1. Delete Chunks
		chunkQL := fmt.Sprintf("DELETE %s WHERE file IN %s;", schema.TableFileChunk, idList)
		if _, err := dbClient.Execute(chunkQL); err != nil {
			logger.Warn("Failed to delete chunks: %v", err)
		}

		// 2. Delete Files
		fileQL := fmt.Sprintf("DELETE %s;", idList) // DELETE [id1, id2]; works in SurrealDB
		if _, err := dbClient.Execute(fileQL); err != nil {
			logger.Warn("Failed to delete files: %v", err)
		}
	}

	return nil
}

func processFile(ctx context.Context, dbClient db.Executor, path string) error {
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

	// 2. Check if changed / check if version already exists

	// We want to know:
	// 1. Current hash in DB for this file.
	// 2. Does this specific hash already exist in file_chunks?

	// Fetch current state
	var currentDBHash string
	ql := fmt.Sprintf("SELECT hash FROM %s;", fileID)
	res, err := dbClient.Execute(ql)
	if err == nil {
		if rows, ok := res.([]interface{}); ok && len(rows) > 0 {
			if row, ok := rows[0].(map[string]interface{}); ok {
				currentDBHash, _ = row["hash"].(string)
			}
		}
	}

	// Optimization: If current DB hash matches disk hash, we are 100% up to date.
	if currentDBHash == hash {
		return nil
	}

	logger.Info("Processing %s...", filepath.Base(path))

	// 3. Update File Node (Update pointer to current version)
	_, err = dbClient.Execute(fmt.Sprintf("UPDATE %s SET hash = '%s', path = '%s';", fileID, hash, db.EscapeSQL(path)))
	if err != nil {
		return fmt.Errorf("db update error: %w", err)
	}

	// 4. Check if we already have chunks for this hash (from history or another branch)
	// We assume if one chunk exists for this hash, they all do.
	checkQL := fmt.Sprintf("SELECT count() FROM %s WHERE file = %s AND hash = '%s';", schema.TableFileChunk, fileID, hash)
	checkRes, err := dbClient.Execute(checkQL)
	if err == nil {
		// SurrealDB count returns [{ count: N }]
		if rows, ok := checkRes.([]interface{}); ok && len(rows) > 0 {
			if row, ok := rows[0].(map[string]interface{}); ok {
				// Handle float64 or int (json unmarshal default is float64)
				var count int64
				if c, ok := row["count"].(float64); ok { count = int64(c) }
				if c, ok := row["count"].(int64); ok { count = c }
				if c, ok := row["count"].(int); ok { count = int64(c) }

				if count > 0 {
					logger.Info("  - Using existing embeddings for hash %s", hash[:8])
					return nil // Already have embeddings for this version!
				}
			}
		}
	}

	// 5. Chunk & Persist (Wait for Async Batch)
	// Reset file pointer
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("seek error: %w", err)
	}

	chunks, err := chunkContent(f, 1000)
	if err != nil {
		return fmt.Errorf("chunking error: %w", err)
	}

	// NOTE: We do NOT delete old chunks anymore. We keep history.

	// Batching Logic
	batchSize := 100
	for i := 0; i < len(chunks); i += batchSize {
		end := i + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		batch := chunks[i:end]

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

		// Store Chunks with batch_status = 'pending'
		// Use idempotent CREATE IF NOT EXISTS logic via IF statement to avoid "record already exists" errors
		// and to preserve existing embeddings if we are resuming a partial ingest.
		var qlBuilder strings.Builder
		qlBuilder.WriteString("BEGIN TRANSACTION; ")

		for k, chunkContentStr := range validBatch {
			originalIndex := validIndices[k]
			chunkID := fmt.Sprintf("%s:%s_%s_%d", schema.TableFileChunk, db.SanitizeID(path), hash, originalIndex)

			contentBytes, _ := json.Marshal(chunkContentStr)

			// Logic: IF (SELECT * FROM chunkID) IS EMPTY THEN CREATE chunkID ... END
			// This ensures we only insert if it's missing, effectively an INSERT IGNORE.
			ql := fmt.Sprintf(`IF array::len((SELECT * FROM %s)) = 0 THEN CREATE %s SET file=%s, hash='%s', content=%s, embedding=NONE, batch_status='pending'; END; `,
				chunkID, chunkID, fileID, hash, string(contentBytes))

			qlBuilder.WriteString(ql)
		}

		qlBuilder.WriteString("COMMIT;")

		if _, err := dbClient.Execute(qlBuilder.String()); err != nil {
			logger.Error("Failed to persist pending chunks for %s: %v", path, err)
		}
		logger.Info("  - Queued %d/%d chunks for %s", end, len(chunks), filepath.Base(path))
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
