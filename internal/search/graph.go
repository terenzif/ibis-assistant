package search

import (
	"encoding/json"
	"fmt"

	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
)

// GraphContext represents the historical context of a file
type GraphContext struct {
	Commits []CommitSummary `json:"commits"`
	Issues  []IssueSummary  `json:"issues"`
}

type CommitSummary struct {
	Hash    string `json:"hash"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}

type IssueSummary struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Status  string `json:"status"`
}

// GetFileContext gives us the "Story" of a file from the graph
func GetFileContext(dbClient db.Executor, filePath string) (*GraphContext, error) {
	// Query logic:
	// From the file node, traverse backwards via 'changed' to get commits.
	// For each commit, traverse backwards via 'authored' to get author name.
	// For each commit, traverse forwards via 'implements' to get linked issues.
	// We limit to the most recent 5 commits.

	ql := `
	SELECT
		<-changed<-commit.{
			hash,
			message,
			date,
			author: <-authored<-author.name,
			issues: ->implements->issue.{id, subject, status}
		} as history
	FROM file
	WHERE path = $path;
	`

	res, err := dbClient.SmartQuery(ql, map[string]interface{}{
		"path": filePath,
	})
	if err != nil {
		return nil, fmt.Errorf("graph query failed: %w", err)
	}

	graphCtx := &GraphContext{
		Commits: []CommitSummary{},
		Issues:  []IssueSummary{},
	}

	// Parse Result
	// We expect a slice of maps (rows)
	rows, ok := res.([]interface{})
	if !ok {
		// If empty or unexpected structure
		return graphCtx, nil
	}

	if len(rows) == 0 {
		return graphCtx, nil
	}

	// We only expect one row since path is unique
	row, ok := rows[0].(map[string]interface{})
	if !ok {
		return graphCtx, nil
	}

	// "history" is the projected field containing array of commits
	historyRaw, ok := row["history"]
	if !ok || historyRaw == nil {
		return graphCtx, nil
	}

	// Convert to JSON and back to struct to avoid manual type assertion hell
	bytes, err := json.Marshal(historyRaw)
	if err != nil {
		logger.Error("Failed to marshal graph history: %v", err)
		return graphCtx, nil
	}

	// We define a temporary structure that matches the query projection
	type tempCommit struct {
		Hash    string `json:"hash"`
		Message string `json:"message"`
		Date    string `json:"date"`
		Author  []string `json:"author"` // Graph traversal often returns array even if single edge
		Issues  []struct {
			ID      interface{} `json:"id"` // ID can be "issue:123" or just 123 depending on formatting
			Subject string      `json:"subject"`
			Status  string      `json:"status"` // Status is a string in the DB (issue.status = "Open")
		} `json:"issues"`
	}

	var tempCommits []tempCommit
	if err := json.Unmarshal(bytes, &tempCommits); err != nil {
		logger.Error("Failed to unmarshal graph history: %v", err)
		return graphCtx, nil
	}

	// Flatten and Deduplicate
	issueMap := make(map[string]IssueSummary)

	// Sort commits by date desc? The query didn't order them explicitly inside the projection.
	// We will rely on the client to sort or assume DB returns in some order (often insertion).
	// Let's just process them.

	for _, tc := range tempCommits {
		authorName := "Unknown"
		if len(tc.Author) > 0 {
			authorName = tc.Author[0]
		}

		c := CommitSummary{
			Hash:    tc.Hash,
			Message: tc.Message,
			Date:    tc.Date,
			Author:  authorName,
		}
		graphCtx.Commits = append(graphCtx.Commits, c)

		for _, iss := range tc.Issues {
			// ID processing
			idStr := fmt.Sprintf("%v", iss.ID)

			is := IssueSummary{
				ID:      idStr,
				Subject: iss.Subject,
				Status:  iss.Status,
			}
			issueMap[idStr] = is
		}
	}

	// Collect unique issues
	for _, is := range issueMap {
		graphCtx.Issues = append(graphCtx.Issues, is)
	}

	// Limit commits to 5
	if len(graphCtx.Commits) > 5 {
		graphCtx.Commits = graphCtx.Commits[:5]
	}

	return graphCtx, nil
}
