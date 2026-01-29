package git

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/ingest/redmine"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

// IngestRepo analyzes a git repository and populates the Knowledge Graph
func IngestRepo(client db.Executor, redmineClient redmine.Ingester, repoPath string) error {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}
	repoName := filepath.Base(absPath)

	logger.Info("Starting ingestion for repo: %s (%s)", repoName, absPath)

	// 1. Run Git Log
	// Format: COMMIT|Hash|Parents|Author|Date|Subject
	cmd := exec.Command("git", "log", "--all", "--name-only", "--reverse", "--format=COMMIT|%H|%P|%an|%aI|%s")
	cmd.Dir = absPath
	// Increase buffer for large repos if needed, but standard pipe is usually fine for streaming
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start git command: %w", err)
	}

	// 2. Stream & Parse
	scanner := bufio.NewScanner(stdout)
	// Buffer size for long lines (though file paths shouldn't be huge)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var (
		currentCommitID string
		batchQL         strings.Builder
		batchCount      int
	)

	// Register Repo Node
	// Upsert query for Repo
	// In SurrealDB, ID can be `repo:deckonline`
	repoID := fmt.Sprintf("%s:%s", schema.TableRepo, sanitizeID(repoName))
	// We init the repo node
	logger.Debug("Upserting repo node: %s", repoID)
	_, err = client.Execute(fmt.Sprintf("UPDATE %s SET path = '%s';", repoID, escapeSQL(absPath)))
	if err != nil {
		return fmt.Errorf("failed to upsert repo node: %w", err)
	}

	flushBatch := func() error {
		if batchQL.Len() == 0 {
			return nil
		}
		// Execute transaction
		ql := "BEGIN TRANSACTION;\n" + batchQL.String() + "COMMIT TRANSACTION;"
		logger.Debug("Flushing batch of %d operations", batchCount)
		_, err := client.Execute(ql)
		if err != nil {
			return fmt.Errorf("batch execution failed: %w", err)
		}
		batchQL.Reset()
		batchCount = 0
		return nil
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "COMMIT|") {
			parts := strings.SplitN(line, "|", 6)
			if len(parts) < 6 {
				continue
			}
			// COMMIT|Hash|Parents|Author|Date|Subject
			hash := parts[1]
			parents := strings.Fields(parts[2]) // Split by space
			authorName := parts[3]
			date := parts[4]
			subject := parts[5]

			commitID := fmt.Sprintf("%s:%s", schema.TableCommit, hash)
			currentCommitID = commitID

			// Author Node
			authorID := fmt.Sprintf("%s:%s", schema.TableAuthor, sanitizeID(authorName))

			// --- Batch Construction ---
			
			// 1. Upsert Author
			batchQL.WriteString(fmt.Sprintf("UPDATE %s SET name = '%s';\n", authorID, escapeSQL(authorName)))

			// 2. Create Commit
			batchQL.WriteString(fmt.Sprintf("CREATE %s SET hash = '%s', date = '%s', message = '%s', repo = %s;\n", 
				commitID, hash, date, escapeSQL(subject), repoID))
			
			// 3. Link Author -> Commit
			batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s;\n", authorID, schema.EdgeAuthored, commitID))

			// 4. Link Parents (Timeline)
			for _, pHash := range parents {
				parentID := fmt.Sprintf("%s:%s", schema.TableCommit, pHash)
				batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s;\n", parentID, schema.EdgeParentOf, commitID))
			}

			// 5. Link Issue (Simple linking based on regex-like scan)
			// Scanning for # followed by digits
			msgLen := len(subject)
			for i := 0; i < msgLen; i++ {
				if subject[i] == '#' {
					// Check if next chars are digits
					start := i + 1
					end := start
					for end < msgLen && subject[end] >= '0' && subject[end] <= '9' {
						end++
					}
					if end > start {
						// Found an issue ID
						issueIDStr := subject[start:end]
						// Create Issue Node (Placeholder first)
						issueID := fmt.Sprintf("%s:%s", schema.TableIssue, issueIDStr)
						
						// In-Band Ingestion: Trigger Redmine fetch if client is available
						if redmineClient != nil {
							go func(id string) {
								if err := redmineClient.IngestIssue(client, id); err != nil {
									logger.Warn("Failed to ingest referenced issue #%s: %v", id, err)
								}
							}(issueIDStr)
						} else {
						    // Just ensure existence
						    batchQL.WriteString(fmt.Sprintf("UPDATE %s SET id = %s;\n", issueID, issueIDStr))
						}

						// Link Commit -> Issue
						batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s;\n", commitID, schema.EdgeImplements, issueID))
					}
					i = end // Advance
				}
			}

		} else {
			// File line
			path := line
			// Handle quoted paths from git log (e.g. "path/to/file with spaces.txt")
			if strings.HasPrefix(path, "\"") && strings.HasSuffix(path, "\"") {
				if unquoted, err := strconv.Unquote(path); err == nil {
					path = unquoted
				} else {
					logger.Warn("Failed to unquote git path: %s, err: %v", path, err)
				}
			}

			fileID := fmt.Sprintf("%s:%s", schema.TableFile, sanitizeID(path))
			
			// 1. Upsert File
			batchQL.WriteString(fmt.Sprintf("UPDATE %s SET path = '%s';\n", fileID, escapeSQL(path)))
			
			// 3. Link Commit -> File (Changed)
			if currentCommitID != "" {
				batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s;\n", currentCommitID, schema.EdgeChanged, fileID))
			}
		}

		batchCount++
		if batchCount >= 1000 {
			if err := flushBatch(); err != nil {
				return err
			}
			logger.Info("... flushed 1000 items to DB")
		}
	}

	// Final flush
	if err := flushBatch(); err != nil {
		return err
	}
	logger.Debug("Final batch flushed.")

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git command finished with error: %w", err)
	}

	logger.Info("Ingestion complete for %s", repoName)
	return nil
}


func sanitizeID(s string) string {
	// Simple sanitizer for SurrealDB IDs (alphanumeric + _ ideally)
	// We'll just replace bad chars with _
	// This is critical because `file:path/to/file` is valid ONLY if escaped with ⟨ ⟩, 
	// but standard IDs easiest if safe.
	// Actually, Surreal handles `table:⟨complex value⟩`.
	// For simplicity in this generated code, we'll just hash or safe-encode if it gets complex.
	// Let's rely on ReplaceAll for now.
	safe := strings.ReplaceAll(s, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	safe = strings.ReplaceAll(safe, ".", "_")
	safe = strings.ReplaceAll(safe, "-", "_")
	safe = strings.ReplaceAll(safe, " ", "_")
	safe = strings.ReplaceAll(safe, "'", "")
	safe = strings.ReplaceAll(safe, "\"", "")
	return strings.ToLower(safe)
}

func escapeSQL(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}
