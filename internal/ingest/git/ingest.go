package git

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/ingest/redmine"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

// IngestRepo analyzes a git repository and populates the Knowledge Graph
func IngestRepo(client db.Executor, redmineClient redmine.Ingester, repoPath string, concurrency int) error {
	if client == nil {
		return fmt.Errorf("database client is nil")
	}
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}
	repoName := filepath.Base(absPath)

	logger.Info("Starting ingestion for repo: %s (%s)", repoName, absPath)

	// Register Repo Node
	repoID := fmt.Sprintf("%s:%s", schema.TableRepo, db.SanitizeID(repoName))
	logger.Debug("Upserting repo node: %s", repoID)
	_, err = client.Execute(fmt.Sprintf("UPDATE %s SET path = '%s';", repoID, db.EscapeSQL(absPath)))
	if err != nil {
		return fmt.Errorf("failed to upsert repo node: %w", err)
	}

	// 1. Start Hash Generator (Stream all commits)
	cmdHashes := exec.Command("git", "log", "--all", "--reverse", "--format=%H")
	cmdHashes.Dir = absPath
	stdoutHashes, err := cmdHashes.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create hashes stdout pipe: %w", err)
	}
	if err := cmdHashes.Start(); err != nil {
		return fmt.Errorf("failed to start git log hashes: %w", err)
	}

	// 2. Start Ingest Worker (Consumes new hashes, produces log output)
	// We use --no-walk --stdin to ingest only specific commits provided on stdin.
	cmdIngest := exec.Command("git", "log", "--no-walk", "--stdin", "--numstat", "--format=COMMIT|%H|%P|%an|%aI|%s")
	cmdIngest.Dir = absPath
	stdinIngest, err := cmdIngest.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create ingest stdin pipe: %w", err)
	}
	stdoutIngest, err := cmdIngest.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create ingest stdout pipe: %w", err)
	}
	// stderr for debugging
	// cmdIngest.Stderr = os.Stderr

	if err := cmdIngest.Start(); err != nil {
		return fmt.Errorf("failed to start git log ingest: %w", err)
	}

	// Channel to signal feeder completion/error
	feederErrChan := make(chan error, 1)

	// Goroutine: Feed Hashes to Ingest Worker
	go func() {
		defer stdinIngest.Close() // Close stdin to signal we are done feeding

		scanner := bufio.NewScanner(stdoutHashes)
		batchSize := 500
		var batch []string

		// Pre-allocate reusable buffers for batch processing to reduce GC pressure
		ids := make([]string, 0, batchSize)
		idMap := make(map[string]bool)
		vars := make(map[string]interface{}, 1)

		processBatch := func() error {
			if len(batch) == 0 {
				return nil
			}

			// Clear maps and reset slice
			for k := range idMap {
				delete(idMap, k)
			}
			ids = ids[:0]

			// Check DB for existing commits
			// We can't pass 500 IDs in a single query if the string is too long?
			// 500 * 40 chars = 20KB. Fine.

			// Build ID list
			// "SELECT id FROM commit WHERE id IN ['commit:hash1', 'commit:hash2', ...]"
			for _, h := range batch {
				id := fmt.Sprintf("%s:%s", schema.TableCommit, h)
				ids = append(ids, id)
			}

			// SmartQuery with array param?
			// SurrealDB: "SELECT id FROM commit WHERE id INSIDE $ids"
			// But SmartQuery vars handling depends on driver.
			// Let's assume we can pass a slice of strings.

			// Optimization: If we query, we get back IDs that EXIST.
			// The ones NOT in the result are NEW.

			// If slice param fails, we might need to construct the query string manually or loops.
			// Trying SmartQuery with slice.

			vars["ids"] = ids
			resRaw, err := client.SmartQuery("SELECT VALUE id FROM commit WHERE id IN $ids", vars)
			if err != nil {
				return fmt.Errorf("failed to check existing commits: %w", err)
			}

			// Parse result to find existing (Optimized: Direct Type Assertion for SELECT VALUE)
			if results, ok := resRaw.([]interface{}); ok {
				for _, item := range results {
					if id, ok := item.(string); ok {
						idMap[id] = true
					}
				}
			}

			// Identify New
			var newCount int
			for _, h := range batch {
				id := fmt.Sprintf("%s:%s", schema.TableCommit, h)
				if !idMap[id] {
					// Is new, write to ingest stdin
					if _, err := io.WriteString(stdinIngest, h+"\n"); err != nil {
						return fmt.Errorf("failed to write to ingest stdin: %w", err)
					}
					newCount++
				}
			}

			if newCount > 0 {
				logger.Debug("Batch check: %d/%d new commits queued", newCount, len(batch))
			}

			return nil
		}

		for scanner.Scan() {
			hash := strings.TrimSpace(scanner.Text())
			if hash == "" {
				continue
			}
			batch = append(batch, hash)
			if len(batch) >= batchSize {
				if err := processBatch(); err != nil {
					feederErrChan <- err
					return
				}
				batch = batch[:0]
			}
		}
		// Final batch
		if err := processBatch(); err != nil {
			feederErrChan <- err
			return
		}

		feederErrChan <- nil
	}()

	// 3. Main Thread: Consume Ingest Output
	scannerIngest := bufio.NewScanner(stdoutIngest)
	// Use larger buffer for numstat output
	buf := make([]byte, 0, 64*1024)
	scannerIngest.Buffer(buf, 1024*1024)

	// We pass nil for existingCommits because we already filtered them!
	ingestErr := processGitLogStream(context.Background(), scannerIngest, client, redmineClient, repoID, concurrency)

	// Wait for feeder
	feederErr := <-feederErrChan

	// Wait for commands
	cmdHashes.Wait() // Ignore error here if pipe closed early? usually fine.
	cmdIngest.Wait()

	if feederErr != nil {
		return fmt.Errorf("feeder error: %w", feederErr)
	}
	if ingestErr != nil {
		return fmt.Errorf("ingest error: %w", ingestErr)
	}

	logger.Info("Ingestion complete for %s", repoName)
	return nil
}

// processGitLogStream consumes the git log output and writes to DB
func processGitLogStream(
	ctx context.Context,
	scanner *bufio.Scanner,
	client db.Executor,
	redmineClient redmine.Ingester,
	repoID string,
	concurrency int,
) error {
	var (
		currentCommitID string
		batchQL         strings.Builder
		batchCount      int
		skipping        bool
	)

	if concurrency < 1 {
		concurrency = 1
	}

	// Worker Pool for Redmine Ingestion
	// Using a buffered channel allows the main loop (Git Log Parsing) to proceed ahead of Redmine ingestion,
	// effectively parallelizing the two stages.
	jobChan := make(chan string, 100)
	var wg sync.WaitGroup

	if redmineClient != nil {
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for id := range jobChan {
					if err := redmineClient.IngestIssue(ctx, client, id); err != nil {
						logger.Warn("Failed to ingest referenced issue #%s: %v", id, err)
					}
				}
			}()
		}
	}

	// Ensure workers are stopped on exit
	defer func() {
		if redmineClient != nil {
			close(jobChan)
			wg.Wait()
		}
	}()

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
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

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

			// Assume filtering already happened upstream in the feeder goroutine
			skipping = false
			currentCommitID = commitID

			// Author Node
			authorID := fmt.Sprintf("%s:%s", schema.TableAuthor, db.SanitizeID(authorName))

			// --- Batch Construction ---

			// 1. Upsert Author
			batchQL.WriteString(fmt.Sprintf("UPDATE %s SET name = '%s';\n", authorID, db.EscapeSQL(authorName)))

			// 2. Create Commit (Using UPDATE to be safe/idempotent)
			batchQL.WriteString(fmt.Sprintf("UPDATE %s SET hash = '%s', date = '%s', message = '%s', repo = %s;\n",
				commitID, hash, date, db.EscapeSQL(subject), repoID))

			// 3. Link Author -> Commit
			batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s;\n", authorID, schema.EdgeAuthored, commitID))

			// 4. Link Parents (Timeline)
			for _, pHash := range parents {
				parentID := fmt.Sprintf("%s:%s", schema.TableCommit, pHash)
				batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s;\n", parentID, schema.EdgeParentOf, commitID))
			}

			// 5. Link Issues
			issueRefs := ExtractIssueRefs(subject)
			for _, ref := range issueRefs {
				issueIDStr := ref.ID
				issueID := fmt.Sprintf("%s:%s", schema.TableIssue, issueIDStr)

				// In-Band Ingestion: Trigger Redmine fetch if client is available
				if redmineClient != nil {
					select {
					case jobChan <- issueIDStr:
					case <-ctx.Done():
					}
				} else {
					// Just ensure existence
					batchQL.WriteString(fmt.Sprintf("UPDATE %s SET id = %s;\n", issueID, issueIDStr))
				}

				batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s SET confidence = %f, usage_weight = 1.0;\n",
					commitID, schema.EdgeImplements, issueID, ref.Confidence))
			}

		} else {
			if skipping {
				continue
			}
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
				path = strings.Join(tabParts[2:], "\t")
			} else {
				// Fallback to Fields if tabs missing (e.g. ecosystem quirks)
				// But path with spaces will break Fields logic.
				// Assuming standard git output.
				// If we fail to parse, skip.
				continue
			}

			// Handle binary
			if addedStr == "-" {
				added = 0
			} else {
				a, _ := strconv.Atoi(addedStr)
				added = a
			}
			if deletedStr == "-" {
				deleted = 0
			} else {
				d, _ := strconv.Atoi(deletedStr)
				deleted = d
			}

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
				if impact > 1.0 {
					impact = 1.0
				} // Normalize? Log10(10)=1, Log10(100)=2.
				// Maybe sigmoid? Or just raw log.
				// Spec says "float 0.0-1.0".
				// Let's limit it. If > 100 lines, impact = 1.0?
				// Let's use a sigmoid-like: x / (x + 20) -> 20 lines = 0.5 impact. 100 lines = 0.83.
				impact = totalChanged / (totalChanged + 50.0)
			}

			fileID := fmt.Sprintf("%s:%s", schema.TableFile, db.SanitizeID(path))

			// 1. Upsert File
			batchQL.WriteString(fmt.Sprintf("UPDATE %s SET path = '%s';\n", fileID, db.EscapeSQL(path)))

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

	return nil
}
