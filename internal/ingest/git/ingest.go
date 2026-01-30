package git

import (
	"bufio"
	"fmt"
	"math"
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
	// We use --numstat to get lines changed.
	// Output format will be:
	// COMMIT|Hash|Parents|Author|Date|Subject
	// Added Deleted Path
	// ...
	cmd := exec.Command("git", "log", "--all", "--numstat", "--reverse", "--format=COMMIT|%H|%P|%an|%aI|%s")
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
	repoID := fmt.Sprintf("%s:%s", schema.TableRepo, sanitizeID(repoName))
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

						// Link Commit -> Issue with default confidence/weight
						// Spec: 0.5 if just mentioned. 1.0 if "Fixes".
						// For now, default to 1.0 for simplicity or parse properly.
						// Let's do simple keyword check.
						confidence := 0.5
						lowerSub := strings.ToLower(subject)
						if strings.Contains(lowerSub, "fix") || strings.Contains(lowerSub, "close") || strings.Contains(lowerSub, "resolve") {
							confidence = 1.0
						}

						batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s SET confidence = %f, usage_weight = 1.0;\n",
							commitID, schema.EdgeImplements, issueID, confidence))
					}
					i = end // Advance
				}
			}

		} else {
			// Numstat line: Added Deleted Path
			// e.g. "5       3       src/main.go"
			// Binary files: "-       -       image.png"
			parts := strings.Fields(line)
			if len(parts) < 3 {
				continue
			}

			addedStr := parts[0]
			deletedStr := parts[1]
			// Path is the rest (could have spaces if not properly separated, but fields splits by whitespace)
			// Wait, Fields splits by whitespace. Git --numstat output uses TABS between numbers and path,
			// but path can contain spaces. If path contains spaces, it is NOT quoted in numstat unless weird config.
			// Actually, git log --numstat separates by TAB.

			// Let's re-parse using Tab delimiter for safety if possible, but scanner.Text() gives a string.
			// Standard git numstat: <added>\t<deleted>\t<path>
			// Let's try splitting by tab.
			tabParts := strings.Split(line, "\t")
			var path string
			var added, deleted int

			if len(tabParts) >= 3 {
				addedStr = tabParts[0]
				deletedStr = tabParts[1]
				path = tabParts[2]
			} else {
				// Fallback to Fields if tabs missing (e.g. ecosystem quirks)
				// But path with spaces will break Fields logic.
				// Assuming standard git output.
				// If we fail to parse, skip.
				continue
			}

			// Handle binary
			if addedStr == "-" { added = 0 } else { a, _ := strconv.Atoi(addedStr); added = a }
			if deletedStr == "-" { deleted = 0 } else { d, _ := strconv.Atoi(deletedStr); deleted = d }

			// Handle quoted paths
			if strings.HasPrefix(path, "\"") && strings.HasSuffix(path, "\"") {
				if unquoted, err := strconv.Unquote(path); err == nil {
					path = unquoted
				}
			}

			// Calculate Impact
			// Logic: log(lines_changed + 1) normalized?
			// Spec says: "Calculate based on lines changed".
			// Let's use log10 to dampen huge diffs.
			totalChanged := float64(added + deleted)
			impact := 0.0
			if totalChanged > 0 {
				impact = math.Log10(totalChanged + 1)
				if impact > 1.0 { impact = 1.0 } // Normalize? Log10(10)=1, Log10(100)=2.
				// Maybe sigmoid? Or just raw log.
				// Spec says "float 0.0-1.0".
				// Let's limit it. If > 100 lines, impact = 1.0?
				// Let's use a sigmoid-like: x / (x + 20) -> 20 lines = 0.5 impact. 100 lines = 0.83.
				impact = totalChanged / (totalChanged + 50.0)
			}

			fileID := fmt.Sprintf("%s:%s", schema.TableFile, sanitizeID(path))
			
			// 1. Upsert File
			batchQL.WriteString(fmt.Sprintf("UPDATE %s SET path = '%s';\n", fileID, escapeSQL(path)))
			
			// 3. Link Commit -> File (Changed) with Impact
			if currentCommitID != "" {
				batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s SET impact = %f, added = %d, deleted = %d;\n",
					currentCommitID, schema.EdgeChanged, fileID, impact, added, deleted))
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
