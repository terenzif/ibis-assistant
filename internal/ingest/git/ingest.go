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

	"github.com/terenzif/ibis-arc/internal/db"
	"github.com/terenzif/ibis-arc/internal/logger"
	"github.com/terenzif/ibis-arc/internal/schema"
	"github.com/terenzif/ibis-arc/internal/ticketing"
)

// IngestRepo analyzes the Git history and structure of a repository, populating the Knowledge Graph with commits, authors, and file relationships.
func IngestRepo(ctx context.Context, client db.Executor, ticketIngester ticketing.Ingester, repoPath string, repoName string, concurrency int, customPatterns ...string) error {
	logger.Info("Ingesting git repository: %s (Name: %s)", repoPath, repoName)
	if client == nil {
		return fmt.Errorf("database client is nil")
	}
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}

	repoID := db.FormatRecordID(schema.TableRepo, db.SanitizeID(repoName))
	_, err = client.Execute(ctx, fmt.Sprintf("UPSERT %s SET name = '%s';", repoID, db.EscapeSQL(repoName)))
	if err != nil {
		return fmt.Errorf("failed to upsert repo: %w", err)
	}

	cmdHashes := exec.CommandContext(ctx, "git", "log", "HEAD", "--reverse", "--format=%H")
	cmdHashes.Dir = absPath
	stdoutHashes, err := cmdHashes.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create hashes stdout pipe: %w", err)
	}
	if err := cmdHashes.Start(); err != nil {
		return fmt.Errorf("failed to start git log hashes: %w", err)
	}

	cmdIngest := exec.CommandContext(ctx, "git", "log", "--no-walk", "--stdin", "--numstat", "--format=COMMIT|%H|%P|%an|%aI|%s")
	cmdIngest.Dir = absPath
	stdinIngest, err := cmdIngest.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create ingest stdin pipe: %w", err)
	}
	stdoutIngest, err := cmdIngest.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create ingest stdout pipe: %w", err)
	}

	if err := cmdIngest.Start(); err != nil {
		return fmt.Errorf("failed to start git log ingest: %w", err)
	}

	feederErrChan := make(chan error, 1)

	go func() {
		defer stdinIngest.Close()

		scanner := bufio.NewScanner(stdoutHashes)
		batchSize := 500
		var batch []string
		ids := make([]string, 0, batchSize)
		idMap := make(map[string]bool)
		vars := make(map[string]interface{}, 1)

		processBatch := func() error {
			if len(batch) == 0 {
				return nil
			}

			for k := range idMap {
				delete(idMap, k)
			}
			ids = ids[:0]
			for _, h := range batch {
				id := db.FormatRecordID(schema.TableCommit, h)
				ids = append(ids, id)
			}

			vars["ids"] = ids
			resRaw, err := client.SmartQuery(ctx, "SELECT VALUE id FROM commit WHERE id IN $ids", vars)
			if err != nil {
				return fmt.Errorf("failed to check existing commits: %w", err)
			}

			if results, ok := resRaw.([]interface{}); ok {
				for _, item := range results {
					if id, ok := item.(string); ok {
						idMap[id] = true
					}
				}
			}

			newCount := 0
			for _, h := range batch {
				id := db.FormatRecordID(schema.TableCommit, h)
				if !idMap[id] {
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
		if err := processBatch(); err != nil {
			feederErrChan <- err
			return
		}

		feederErrChan <- nil
	}()

	scannerIngest := bufio.NewScanner(stdoutIngest)
	buf := make([]byte, 0, 64*1024)
	scannerIngest.Buffer(buf, 1024*1024)

	ingestErr := processGitLogStream(ctx, scannerIngest, client, ticketIngester, repoID, repoName, concurrency, customPatterns)
	feederErr := <-feederErrChan

	cmdHashes.Wait()
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
	ticketIngester ticketing.Ingester,
	repoID string,
	repoName string,
	concurrency int,
	customPatterns []string,
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

	jobChan := make(chan ticketing.IssueReference, 100)
	var wg sync.WaitGroup
	seenIssues := make(map[string]bool)
	var filesInCommit []string

	if ticketIngester != nil {
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ref := range jobChan {
					projectKey := ref.ProjectKey
					if projectKey == "" {
						projectKey = repoName
					}
					if err := ticketIngester.IngestIssueReference(ctx, projectKey, ref); err != nil {
						logger.Warn("Failed to ingest referenced ticket %s (%s): %v", ref.ExternalKey, ref.Provider, err)
					}
				}
			}()
		}
	}

	defer func() {
		if ticketIngester != nil {
			close(jobChan)
			wg.Wait()
		}
	}()

	flushBatch := func() error {
		if batchQL.Len() == 0 {
			return nil
		}
		ql := "BEGIN TRANSACTION;\n" + batchQL.String() + "COMMIT TRANSACTION;"
		logger.Debug("Flushing batch of %d operations", batchCount)
		_, err := client.Execute(ctx, ql)
		if err != nil {
			return fmt.Errorf("batch execution failed: %w", err)
		}
		batchQL.Reset()
		batchCount = 0
		return nil
	}

	for scanner.Scan() {
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
			hash := parts[1]
			parents := strings.Fields(parts[2])
			authorName := parts[3]
			date := parts[4]
			subject := parts[5]

			commitID := db.FormatRecordID(schema.TableCommit, hash)
			skipping = false
			currentCommitID = commitID
			filesInCommit = []string{}
			authorID := db.FormatRecordID(schema.TableAuthor, db.SanitizeID(authorName))

			batchQL.WriteString(fmt.Sprintf("UPDATE %s SET name = '%s';\n", authorID, db.EscapeSQL(authorName)))
			batchQL.WriteString(fmt.Sprintf("UPDATE %s SET hash = '%s', date = '%s', message = '%s', repo = %s, batch_status = 'pending';\n",
				commitID, hash, date, db.EscapeSQL(subject), repoID))
			batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s;\n", authorID, schema.EdgeAuthored, commitID))

			for _, pHash := range parents {
				parentID := db.FormatRecordID(schema.TableCommit, pHash)
				batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s;\n", parentID, schema.EdgeParentOf, commitID))
			}

			issueRefs := ExtractIssueRefs(subject, customPatterns...)
			for _, ref := range issueRefs {
				ticketRef := ticketing.IssueReference{
					Provider:    ref.Provider,
					ExternalID:  ref.ID,
					ExternalKey: ref.Key,
					ProjectKey:  ref.ProjectKey,
					Confidence:  ref.Confidence,
				}
				if ticketRef.ProjectKey == "" {
					ticketRef.ProjectKey = repoName
				}

				resolvedRef := ticketRef
				if ticketIngester != nil {
					resolved, err := ticketIngester.ResolveReference(ticketRef.ProjectKey, ticketRef)
					if err != nil {
						logger.Warn("Failed to resolve ticket reference %+v: %v", ticketRef, err)
						continue
					}
					resolvedRef = resolved
				} else if resolvedRef.Provider == "" {
					resolvedRef.Provider = ticketing.ProviderRedmine
				}

				if resolvedRef.ExternalKey == "" {
					resolvedRef.ExternalKey = resolvedRef.ExternalID
				}
				if resolvedRef.ExternalID == "" {
					resolvedRef.ExternalID = resolvedRef.ExternalKey
				}
				if resolvedRef.Provider == "" {
					resolvedRef.Provider = ticketing.ProviderRedmine
				}

				issueID := ticketing.IssueRecordID(resolvedRef.Provider, resolvedRef.ExternalKey)
				dedupeKey := fmt.Sprintf("%s|%s", resolvedRef.Provider, resolvedRef.ExternalKey)

				if ticketIngester != nil {
					if !seenIssues[dedupeKey] {
						seenIssues[dedupeKey] = true
						select {
						case jobChan <- resolvedRef:
						case <-ctx.Done():
						}
					}
				} else {
					batchQL.WriteString(fmt.Sprintf("UPDATE %s SET provider = '%s', external_id = '%s', external_key = '%s';\n",
						issueID,
						db.EscapeSQL(string(resolvedRef.Provider)),
						db.EscapeSQL(resolvedRef.ExternalID),
						db.EscapeSQL(resolvedRef.ExternalKey),
					))
				}

				batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s SET confidence = %f, usage_weight = 1.0;\n",
					commitID, schema.EdgeImplements, issueID, ref.Confidence))
			}

		} else {
			if skipping {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) < 3 {
				continue
			}

			addedStr := parts[0]
			deletedStr := parts[1]
			tabParts := strings.Split(line, "\t")
			var path string
			var added, deleted int

			if len(tabParts) >= 3 {
				addedStr = tabParts[0]
				deletedStr = tabParts[1]
				path = strings.Join(tabParts[2:], "\t")
			} else {
				continue
			}

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

			if strings.HasPrefix(path, "\"") && strings.HasSuffix(path, "\"") {
				if unquoted, err := strconv.Unquote(path); err == nil {
					path = unquoted
				}
			}

			totalChanged := float64(added + deleted)
			impact := 0.0
			if totalChanged > 0 {
				impact = math.Log10(totalChanged + 1)
				if impact > 1.0 {
					impact = 1.0
				}
				impact = totalChanged / (totalChanged + 50.0)
			}

			safeRepoName := db.SanitizeID(repoName)
			fileID := db.FormatRecordID(schema.TableFile, fmt.Sprintf("%s_%s", safeRepoName, db.SanitizeID(path)))
			batchQL.WriteString(fmt.Sprintf("UPDATE %s SET path = '%s';\n", fileID, db.EscapeSQL(path)))

			if currentCommitID != "" {
				batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s SET impact = %f, added = %d, deleted = %d;\n",
					currentCommitID, schema.EdgeChanged, fileID, impact, added, deleted))

				for _, prevFileID := range filesInCommit {
					batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s SET commit = %s;\n",
						fileID, schema.EdgeCoupledWith, prevFileID, currentCommitID))
					batchQL.WriteString(fmt.Sprintf("RELATE %s->%s->%s SET commit = %s;\n",
						prevFileID, schema.EdgeCoupledWith, fileID, currentCommitID))
				}
				filesInCommit = append(filesInCommit, fileID)
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

	if err := flushBatch(); err != nil {
		return err
	}
	logger.Debug("Final batch flushed.")

	return nil
}
