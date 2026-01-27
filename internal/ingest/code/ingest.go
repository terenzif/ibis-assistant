package code

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

type AIEmbedder interface {
	BatchEmbedText(texts []string) ([][]float32, error)
}

// SupportedExtensions filters which files we analyze
var SupportedExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".md": true, 
	".cs": true, ".java": true, ".cpp": true, ".h": true, ".c": true,
	".html": true, ".css": true, ".sql": true,
}

// IngestCodebase scans the repo and updates embeddings for changed files
func IngestCodebase(dbClient db.Executor, aiClient AIEmbedder, repoPath string) error {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return err
	}

	log.Printf("Starting code analysis for: %s", absPath)

	var batchQL strings.Builder

	flushBatch := func() error {
		if batchQL.Len() == 0 {
			return nil
		}
		ql := "BEGIN TRANSACTION;\n" + batchQL.String() + "COMMIT TRANSACTION;"
		_, err := dbClient.Execute(ql)
		if err != nil {
			return fmt.Errorf("batch execution failed: %w", err)
		}
		batchQL.Reset()
		return nil
	}

	type pendingFile struct {
		path string
		hash string
	}
	var pendingFiles []pendingFile

	processPending := func() error {
		if len(pendingFiles) == 0 {
			return nil
		}

		// 1. Build IDs for Bulk Query
		ids := make([]string, 0, len(pendingFiles))
		for _, pf := range pendingFiles {
			fileID := fmt.Sprintf("%s:%s", schema.TableFile, sanitizeID(pf.path))
			ids = append(ids, fileID)
		}

		// 2. Query Existing Hashes
		// SurrealDB: SELECT id, hash FROM file WHERE id IN ['id1', 'id2']
		idList := "'" + strings.Join(ids, "', '") + "'"
		ql := fmt.Sprintf("SELECT id, hash FROM %s WHERE id IN [%s];", schema.TableFile, idList)
		res, err := dbClient.Execute(ql)
		if err != nil {
			return fmt.Errorf("failed to check existing files: %w", err)
		}

		existingHashes := make(map[string]string)
		// Parse result (interface{} -> []interface{} -> map[string]interface{})
		if resSlice, ok := res.([]interface{}); ok {
			for _, item := range resSlice {
				if rec, ok := item.(map[string]interface{}); ok {
					id, _ := rec["id"].(string)
					hash, _ := rec["hash"].(string)
					if id != "" && hash != "" {
						existingHashes[id] = hash
					}
				}
			}
		}

		// 3. Process Logic
		for _, pf := range pendingFiles {
			fileID := fmt.Sprintf("%s:%s", schema.TableFile, sanitizeID(pf.path))

			// Delta Check
			if existingHash, exists := existingHashes[fileID]; exists {
				if existingHash == pf.hash {
					continue // Unchanged
				}
			}

			log.Printf("Processing %s...", filepath.Base(pf.path))

			// Update File Node
			batchQL.WriteString(fmt.Sprintf("UPDATE %s SET hash = '%s', path = '%s';\n", fileID, pf.hash, escapeSQL(pf.path)))

			// Read & Chunk
			content, err := os.ReadFile(pf.path)
			if err != nil {
				log.Printf("Error reading %s: %v", pf.path, err)
				continue
			}

			// Delete old chunks
			batchQL.WriteString(fmt.Sprintf("DELETE %s WHERE file = %s;\n", schema.TableFileChunk, fileID))

			chunks := chunkContent(string(content), 1000)

			// Embed & Store (Inner Batching for AI)
			aiBatchSize := 100
			for i := 0; i < len(chunks); i += aiBatchSize {
				end := i + aiBatchSize
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

				vectors, err := aiClient.BatchEmbedText(validBatch)
				if err != nil {
					log.Printf("Embedding error for %s: %v", pf.path, err)
					continue
				}

				for k, vec := range vectors {
					originalIndex := validIndices[k]
					chunkContentStr := validBatch[k]
					vecJson, _ := json.Marshal(vec)
					chunkID := fmt.Sprintf("%s:%s_%d", schema.TableFileChunk, sanitizeID(pf.path), originalIndex)

					ql := fmt.Sprintf("CREATE %s SET file = %s, content = '%s', embedding = %s;",
						chunkID, fileID, escapeSQL(chunkContentStr), string(vecJson))
					batchQL.WriteString(ql + "\n")
				}
			}
		}

		pendingFiles = pendingFiles[:0]

		// Flush DB Writes if buffer gets large
		if batchQL.Len() > 64*1024 {
			if err := flushBatch(); err != nil {
				return err
			}
		}
		return nil
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

		// Calculate Hash
		hash, err := fileHash(path)
		if err != nil {
			log.Printf("Error hashing %s: %v", path, err)
			return nil
		}

		// Add to pending
		pendingFiles = append(pendingFiles, pendingFile{path: path, hash: hash})
		
		// Delete old chunks
		// DELETE file_chunk WHERE file = $fileID
		dbClient.Execute(fmt.Sprintf("DELETE %s WHERE file = %s;", schema.TableFileChunk, fileID))

		// Batching Logic (Gemini limit is 100 per batch)
		batchSize := 100
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

			// Call Batch API
			vectors, err := aiClient.BatchEmbedText(validBatch)
			if err != nil {
				log.Printf("Batch embedding error for %s: %v", path, err)
				continue
			}

			// Store Results
			var batchQL strings.Builder
			batchQL.WriteString("BEGIN TRANSACTION;\n")

			for k, vec := range vectors {
				originalIndex := validIndices[k]
				chunkContentStr := validBatch[k]
				
				vecJson, _ := json.Marshal(vec)
				chunkID := fmt.Sprintf("%s:%s_%d", schema.TableFileChunk, sanitizeID(path), originalIndex)
				
				ql := fmt.Sprintf("CREATE %s SET file = %s, content = '%s', embedding = %s;\n",
					chunkID, fileID, escapeSQL(chunkContentStr), string(vecJson))
				
				batchQL.WriteString(ql)
			}
			batchQL.WriteString("COMMIT TRANSACTION;")

			if _, err := dbClient.Execute(batchQL.String()); err != nil {
				log.Printf("Error storing chunks for %s: %v", path, err)
		if len(pendingFiles) >= 50 {
			if err := processPending(); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	// Final process and flush
	if err := processPending(); err != nil {
		return err
	}
	return flushBatch()
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
