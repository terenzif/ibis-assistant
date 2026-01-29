package code

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

type AIClient interface {
	BatchEmbedText(texts []string) ([][]float32, error)
	IsFunctional() bool
}

// SupportedExtensions filters which files we analyze
var SupportedExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".md": true, 
	".cs": true, ".java": true, ".cpp": true, ".h": true, ".c": true,
	".html": true, ".css": true, ".sql": true,
}

// IngestCodebase scans the repo and updates embeddings for changed files
func IngestCodebase(dbClient db.Executor, aiClient AIClient, repoPath string) error {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return err
	}

	logger.Info("Starting code analysis for: %s", absPath)

	if aiClient == nil || !aiClient.IsFunctional() {
		return fmt.Errorf("AI vectorization is disabled: no Gemini API keys provided (set GEMINI_API_KEY or gemini_keys in config.json)")
	}

	err = filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") || info.Name() == "node_modules" || info.Name() == "bin" || info.Name() == "obj" {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !SupportedExtensions[ext] {
			return nil
		}

		if err := processFile(dbClient, aiClient, path); err != nil {
			logger.Error("Error processing file %s: %v", path, err)
		}
		return nil
	})

	logger.Info("Code analysis complete for %s", absPath)
	return err
}

func processFile(dbClient db.Executor, aiClient AIClient, path string) error {
	// 1. Calculate Hash
	hash, err := fileHash(path)
	if err != nil {
		return fmt.Errorf("hashing error: %w", err)
	}

	fileID := fmt.Sprintf("%s:%s", schema.TableFile, sanitizeID(path))

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
	_, err = dbClient.Execute(fmt.Sprintf("UPDATE %s SET hash = '%s', path = '%s';", fileID, hash, escapeSQL(path)))
	if err != nil {
		return fmt.Errorf("db update error: %w", err)
	}

	// Chunk & Embed
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	chunks := chunkContent(string(content), 1000) // 1000 chars ~ 250 tokens

	// Delete old chunks
	// DELETE file_chunk WHERE file = $fileID
	dbClient.Execute(fmt.Sprintf("DELETE %s WHERE file = %s;", schema.TableFileChunk, fileID))

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
			chunkID := fmt.Sprintf("%s:%s_%d", schema.TableFileChunk, sanitizeID(path), originalIndex)

			ql := fmt.Sprintf("CREATE %s SET file = %s, content = '%s', embedding = %s;",
				chunkID, fileID, escapeSQL(chunkContentStr), string(vecJson))

			dbClient.Execute(ql)
		}
		logger.Info("  - Embedded %d/%d chunks for %s", end, len(chunks), filepath.Base(path))
	}

	return nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func chunkContent(text string, size int) []string {
	// Very naive chunking by lines/size. 
	// Production should use a tokenizer or smarter splitter.
	var chunks []string
	lines := strings.Split(text, "\n")
	var currentChunk strings.Builder
	
	for _, line := range lines {
		if currentChunk.Len() + len(line) > size {
			chunks = append(chunks, currentChunk.String())
			currentChunk.Reset()
		}
		currentChunk.WriteString(line + "\n")
	}
	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}
	return chunks
}

// Duplicated helper (should move to shared utils)
func sanitizeID(s string) string {
	safe := strings.ReplaceAll(s, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	safe = strings.ReplaceAll(safe, ".", "_")
	safe = strings.ReplaceAll(safe, "-", "_")
	safe = strings.ReplaceAll(safe, ":", "_") // Drive letters
	safe = strings.ReplaceAll(safe, " ", "_")
	return strings.ToLower(safe)
}

func escapeSQL(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}
