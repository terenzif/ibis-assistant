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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/terenzif/ibis-arc/internal/config"
	"github.com/terenzif/ibis-arc/internal/db"
	"github.com/terenzif/ibis-arc/internal/logger"
	"github.com/terenzif/ibis-arc/internal/schema"
)

type AIClient interface {
	IsFunctional() bool
}

// IngestCodebase scans the repository and updates vector embeddings for any modified or new files.
func IngestCodebase(ctx context.Context, dbClient db.Executor, aiClient AIClient, repoPath string, repoName string, cfg *config.Config) error {
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
			for path := range pathsChan {
				select {
				case <-ctx.Done():
					return
				default:
					// Continue processing
				}

				relPath, err := filepath.Rel(absPath, path)
				if err != nil {
					logger.Error("Error calculating relative path for %s: %v", path, err)
					continue
				}
				if err := processFile(ctx, dbClient, path, relPath, repoName); err != nil {
					logger.Error("Error processing %s: %v", path, err)
				}
			}
		}()
	}

	// 3. Get Active Files via Git
	activeFilesMap := make(map[string]bool)
	cmd := exec.CommandContext(ctx, "git", "ls-tree", "-r", "HEAD", "--name-only")
	cmd.Dir = absPath
	out, gitErr := cmd.Output()

	if gitErr == nil {
		lines := strings.Split(string(out), "\n")
		for _, relPath := range lines {
			relPath = strings.TrimSpace(relPath)
			if relPath == "" {
				continue
			}

			if ctx.Err() != nil {
				return ctx.Err()
			}

			fullPath := filepath.Join(absPath, relPath)
			info, statErr := os.Stat(fullPath)
			if statErr != nil {
				continue
			}

			if ignoredFiles[info.Name()] || isIgnored(fullPath, absPath, ignorePatterns, false) {
				continue
			}

			ext := strings.ToLower(filepath.Ext(fullPath))
			if !supportedExts[ext] {
				continue
			}

			if info.Size() > maxSize {
				logger.Debug("Skipping large file: %s (%d bytes)", fullPath, info.Size())
				continue
			}

			activeFilesMap[relPath] = true

			select {
			case pathsChan <- fullPath:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	} else {
		// Fallback to directory walk
		logger.Warn("git ls-tree failed for %s (%v). Falling back to directory walk.", absPath, gitErr)
		err = filepath.Walk(absPath, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
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
				if isIgnored(path, absPath, ignorePatterns, true) {
					return filepath.SkipDir
				}
				return nil
			}

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
				return nil
			}

			relPath, _ := filepath.Rel(absPath, path)
			activeFilesMap[filepath.ToSlash(relPath)] = true

			select {
			case pathsChan <- path:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	close(pathsChan) // Signal workers to finish
	wg.Wait()        // Wait for all workers

	if ctx.Err() != nil {
		logger.Info("Code analysis cancelled for %s", absPath)
		return ctx.Err()
	}

	// Prune Phase: Delete files from DB that are no longer active in Git HEAD
	if err := pruneRepo(ctx, dbClient, absPath, activeFilesMap); err != nil {
		logger.Error("Error pruning obsolete files for %s: %v", absPath, err)
	}

	logger.Info("Code analysis complete for %s", absPath)
	return err
}

func pruneRepo(ctx context.Context, dbClient db.Executor, repoPath string, activeFilesMap map[string]bool) error {
	logger.Info("Pruning obsolete files for %s...", repoPath)

	// Fetch all files in this repo from DB
	separator := string(os.PathSeparator)
	queryPrefix := db.EscapeSQL(repoPath + separator)
	ql := fmt.Sprintf("SELECT id, path FROM %s WHERE string::starts_with(path, '%s');", schema.TableFile, queryPrefix)

	res, err := dbClient.Execute(ctx, ql)
	if err != nil {
		return fmt.Errorf("failed to fetch files for pruning: %w", err)
	}

	rows, ok := res.([]interface{})
	if !ok {
		return nil // No results
	}

	var toDelete []string

	for _, r := range rows {
		row, ok := r.(map[string]interface{})
		if !ok {
			continue
		}

		id, _ := row["id"].(string)
		path, _ := row["path"].(string)

		if id == "" || path == "" {
			continue
		}

		// Calculate relative path to match activeFilesMap keys
		relPath, err := filepath.Rel(repoPath, path)
		if err != nil {
			logger.Warn("Failed to get relative path for %s: %v", path, err)
			continue
		}

		// Normalize slashes for git matching (git always uses forward slashes)
		relPathGit := filepath.ToSlash(relPath)

		// If the file is not in activeFilesMap, it means it's not in git ls-tree HEAD (or directory walk).
		if !activeFilesMap[relPathGit] && !activeFilesMap[relPath] {
			logger.Debug("Deleting %s (Not in git HEAD / Walk)", path)
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
			if j > 0 {
				idListBuilder.WriteString(", ")
			}
			idListBuilder.WriteString(id)
		}
		idListBuilder.WriteString("]")
		idList := idListBuilder.String()

		// 1. Delete Chunks
		chunkQL := fmt.Sprintf("DELETE %s WHERE file IN %s;", schema.TableFileChunk, idList)
		if _, err := dbClient.Execute(ctx, chunkQL); err != nil {
			logger.Warn("Failed to delete chunks: %v", err)
		}

		// 2. Delete Files
		fileQL := fmt.Sprintf("DELETE %s;", idList) // DELETE [id1, id2]; works in SurrealDB
		if _, err := dbClient.Execute(ctx, fileQL); err != nil {
			logger.Warn("Failed to delete files: %v", err)
		}
	}

	return nil
}

func processFile(ctx context.Context, dbClient db.Executor, absPath string, relPath string, repoName string) error {
	f, err := os.Open(absPath)
	if err != nil {
		return fmt.Errorf("open error: %w", err)
	}
	defer f.Close()

	// 1. Calculate Hash
	hash, err := fileHash(f)
	if err != nil {
		return fmt.Errorf("hashing error: %w", err)
	}

	// USE REPO-RELATIVE PATH + HASH FOR ID to allow history
	// Prefix with repoName to avoid collisions between multiple repositories
	safeRepoName := db.SanitizeID(repoName)
	fileID := db.FormatRecordID(schema.TableFile, fmt.Sprintf("%s_%s_%s", safeRepoName, db.SanitizeID(relPath), hash))

	// 2. Check if changed / check if version already exists

	// We want to know:
	// 1. Current hash in DB for this file.
	// 2. Does this specific hash already exist in file_chunks?

	// Fetch current state
	var currentDBHash string
	ql := fmt.Sprintf("SELECT hash FROM %s;", fileID)
	res, err := dbClient.Execute(ctx, ql)
	if err == nil {
		if rows, ok := res.([]interface{}); ok && len(rows) > 0 {
			if row, ok := rows[0].(map[string]interface{}); ok {
				currentDBHash, _ = row["hash"].(string)
			}
		}
	}

	// Optimization: If current DB hash matches disk hash, we are 100% up to date.
	if currentDBHash != "" && currentDBHash == hash {
		return nil
	}

	if currentDBHash != "" {
		logger.Debug("Hash mismatch for %s: DB=%s, Disk=%s", relPath, currentDBHash[:8], hash[:8])
	} else {
		logger.Debug("File %s not found in DB or missing hash", relPath)
	}

	logger.Info("Processing %s...", relPath)

	// 3. Update File Node (Update pointer to current version)
	// We store absolute path in the record for local discovery, but the ID is relative.
	_, err = dbClient.Execute(ctx, fmt.Sprintf("UPSERT %s SET hash = '%s', path = '%s', rel_path = '%s', repo_name = '%s';",
		fileID, hash, db.EscapeSQL(absPath), db.EscapeSQL(relPath), db.EscapeSQL(repoName)))
	if err != nil {
		return fmt.Errorf("db update error: %w", err)
	}

	// Check if chunks already exist for this file hash to avoid redundant AI processing and storage.
	checkQL := fmt.Sprintf("SELECT count() FROM %s WHERE file = %s AND hash = '%s';", schema.TableFileChunk, fileID, hash)
	checkRes, err := dbClient.Execute(ctx, checkQL)
	if err == nil {
		// SurrealDB count returns [{ count: N }]
		if rows, ok := checkRes.([]interface{}); ok && len(rows) > 0 {
			if row, ok := rows[0].(map[string]interface{}); ok {
				// Handle float64 or int (json unmarshal default is float64)
				var count int64
				if c, ok := row["count"].(float64); ok {
					count = int64(c)
				}
				if c, ok := row["count"].(int64); ok {
					count = c
				}
				if c, ok := row["count"].(int); ok {
					count = int64(c)
				}

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

	fileBytes, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read error: %w", err)
	}

	var validChunks []ASTChunk
	astChunks, logs, calls, err := ParseAST(ctx, relPath, fileBytes)
	if err == nil && len(astChunks) > 0 {
		validChunks = astChunks
		logger.Debug("  - Extracted %d AST chunks, %d log templates, and %d calls", len(astChunks), len(logs), len(calls))

		// Persist Calls (Semantic Architecture)
		// This translates AST CallEdges into actual Graph Relations (Symbol -> calls -> Symbol)
		for _, call := range calls {
			// We format the ID exactly as we create it in the chunk builder below
			callerSymbolID := db.FormatRecordID(schema.TableSymbol, fmt.Sprintf("%s_%s_%s", safeRepoName, db.SanitizeID(relPath), db.SanitizeID(call.CallerName)))
			calleeSymbolID := db.FormatRecordID(schema.TableSymbol, fmt.Sprintf("%s_%s_%s", safeRepoName, db.SanitizeID(relPath), db.SanitizeID(call.CalleeName)))

			// We issue an upsert for the nodes just in case they haven't been created yet by the chunk logic
			nodeQL := fmt.Sprintf("UPSERT %s SET name = '%s', file = %s; UPSERT %s SET name = '%s', file = %s;",
				callerSymbolID, db.EscapeSQL(call.CallerName), fileID,
				calleeSymbolID, db.EscapeSQL(call.CalleeName), fileID)

			edgeQL := fmt.Sprintf("RELATE %s->%s->%s SET file = '%s', line = %d;",
				callerSymbolID, schema.EdgeCalls, calleeSymbolID, relPath, call.StartLine)
			dbClient.Execute(ctx, nodeQL+edgeQL)
		}
	} else {
		if _, err := f.Seek(0, 0); err != nil {
			return fmt.Errorf("seek error: %w", err)
		}
		rawChunks, err := chunkContent(f, 1000)
		if err != nil {
			return fmt.Errorf("chunking error: %w", err)
		}
		for _, rc := range rawChunks {
			validChunks = append(validChunks, ASTChunk{Content: rc})
		}
	}

	// NOTE: We do NOT delete old chunks anymore. We keep history.

	// 5.1 Persist Log Templates
	if len(logs) > 0 {
		var logBuilder strings.Builder
		logBuilder.WriteString("BEGIN TRANSACTION; ")
		for i, logDef := range logs {
			// ID based on file and line
			logID := db.FormatRecordID(schema.TableLogTemplate, fmt.Sprintf("%s_%s_%d", safeRepoName, db.SanitizeID(relPath), logDef.SourceLine))

			formatBytes, _ := json.Marshal(logDef.FormatString)
			regexBytes, _ := json.Marshal(logDef.Regex)

			ql := fmt.Sprintf("CREATE %s SET format_string=%s, regex=%s, source_file='%s', source_line=%d;",
				logID, string(formatBytes), string(regexBytes), logDef.SourceFile, logDef.SourceLine)
			logBuilder.WriteString(ql)

			// Relate File -> LogTemplate
			relID := db.FormatRecordID(schema.EdgeEmitsLog, fmt.Sprintf("%s_%s_%d", safeRepoName, db.SanitizeID(relPath), i))
			logBuilder.WriteString(fmt.Sprintf("RELATE %s->%s->%s SET id = %s; ", fileID, schema.EdgeEmitsLog, logID, relID))
		}
		logBuilder.WriteString("COMMIT;")
		if _, err := dbClient.Execute(ctx, logBuilder.String()); err != nil {
			logger.Warn("Failed to persist log templates for %s: %v", relPath, err)
		}
	}

	// Batching Logic
	batchSize := 100
	for i := 0; i < len(validChunks); i += batchSize {
		end := i + batchSize
		if end > len(validChunks) {
			end = len(validChunks)
		}
		batch := validChunks[i:end]

		var validBatch []ASTChunk
		var validIndices []int
		for k, c := range batch {
			if strings.TrimSpace(c.Content) != "" {
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

		for k, chunkObj := range validBatch {
			originalIndex := validIndices[k]
			// Chunk ID also uses prefixed relative path
			chunkID := db.FormatRecordID(schema.TableFileChunk, fmt.Sprintf("%s_%s_%s_%d", safeRepoName, db.SanitizeID(relPath), hash, originalIndex))

			contentBytes, _ := json.Marshal(chunkObj.Content)
			symNameBytes, _ := json.Marshal(chunkObj.SymbolName)
			kindBytes, _ := json.Marshal(chunkObj.Kind)

			ql := fmt.Sprintf("CREATE %s SET file=%s, hash='%s', content=%s, symbol_name=%s, kind=%s, start_line=%d, end_line=%d, embedding=NONE, batch_status='pending'; ",
				chunkID, fileID, hash, string(contentBytes), string(symNameBytes), string(kindBytes), chunkObj.StartLine, chunkObj.EndLine)

			qlBuilder.WriteString(ql)

			// If chunk represents a symbol (like function or class), explicitly create a Symbol node
			if chunkObj.Kind != "" && !strings.Contains(chunkObj.Kind, "snippet") && chunkObj.SymbolName != "" {
				symbolID := db.FormatRecordID(schema.TableSymbol, fmt.Sprintf("%s_%s_%s", safeRepoName, db.SanitizeID(relPath), db.SanitizeID(chunkObj.SymbolName)))
				qlBuilder.WriteString(fmt.Sprintf("CREATE %s SET name = %s, kind = %s, file = %s; ", symbolID, string(symNameBytes), string(kindBytes), fileID))
				qlBuilder.WriteString(fmt.Sprintf("RELATE %s->%s->%s; ", fileID, schema.EdgeContains, symbolID))
			}
		}

		qlBuilder.WriteString("COMMIT;")

		if _, err := dbClient.Execute(ctx, qlBuilder.String()); err != nil {
			logger.Error("Failed to persist pending chunks for %s: %v", relPath, err)
		}
		logger.Info("  - Queued %d/%d chunks for %s", end, len(validChunks), relPath)
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
		if currentChunk.Len()+len(line) > size {
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
