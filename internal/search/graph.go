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
	ID      string  `json:"id"`
	Hash    string  `json:"hash"`
	Message string  `json:"message"`
	Author  string  `json:"author"`
	Date    string  `json:"date"`
	Impact  float64 `json:"impact"` // New: 0.0-1.0 score based on lines changed
}

type IssueSummary struct {
	ID          string  `json:"id"`
	Subject     string  `json:"subject"`
	Status      string  `json:"status"`
	UsageWeight float64 `json:"weight"` // New: Reinforcement score
}

// GetFileContext (renamed logic) traverses the weighted graph
func GetFileContext(dbClient db.Executor, filePath string) (*GraphContext, error) {
	// Query logic:
	// 1. Find commits linked via 'changed'. Sort by Date primarily, but could use Impact.
	//    We fetch the edge 'impact' property.
	// 2. From commits, find 'implements' issues.
	//    Filter by 'usage_weight > 0.5' to reduce noise (as per spec).

	ql := `
	SELECT
		<-changed as change_edges,
		<-changed<-commit.{
			id,
			hash,
			message,
			date,
			author: <-authored<-author.name,
			issues: ->implements[WHERE usage_weight > 0.5]->issue.{
				id, subject, status,
				weight: ->implements.usage_weight
			}
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

	rows, ok := res.([]interface{})
	if !ok || len(rows) == 0 {
		return graphCtx, nil
	}
	row, ok := rows[0].(map[string]interface{})
	if !ok {
		return graphCtx, nil
	}

	// Helper to extract impact from edge
	// change_edges is array of edges: { in: ..., out: ..., impact: 0.8 }
	impactMap := make(map[string]float64) // Map commit ID -> Impact
	if edges, ok := row["change_edges"].([]interface{}); ok {
		for _, e := range edges {
			if em, ok := e.(map[string]interface{}); ok {
				// 'out' is the commit ID in <-changed (since commit->changed->file, so in=commit, out=file?
				// Wait. RELATE commit->changed->file.
				// Query: <-changed means we are at file, looking at incoming edges.
				// Incoming edge 'in' is the start node (commit). 'out' is the end node (file).
				if inID, ok := em["in"].(string); ok {
					if imp, ok := em["impact"].(float64); ok {
						impactMap[inID] = imp
					}
				}
			}
		}
	}

	// Parse History
	historyRaw, ok := row["history"]
	if !ok || historyRaw == nil {
		return graphCtx, nil
	}

	bytes, _ := json.Marshal(historyRaw)

	// Temp struct matches projection
	type tempCommit struct {
		ID      string `json:"id"` // Need ID to map impact
		Hash    string `json:"hash"`
		Message string `json:"message"`
		Date    string `json:"date"`
		Author  []string `json:"author"`
		Issues  []struct {
			ID      interface{} `json:"id"`
			Subject string      `json:"subject"`
			Status  string      `json:"status"`
			Weight  []float64   `json:"weight"` // Query ->implements.usage_weight returns array if multiple edges? usually 1.
		} `json:"issues"`
	}

	var tempCommits []tempCommit
	if err := json.Unmarshal(bytes, &tempCommits); err != nil {
		logger.Error("Failed to unmarshal graph history: %v", err)
		return graphCtx, nil
	}

	issueMap := make(map[string]IssueSummary)

	for _, tc := range tempCommits {
		authorName := "Unknown"
		if len(tc.Author) > 0 {
			authorName = tc.Author[0]
		}

		// Lookup impact using Commit ID (which we need to fetch, oops, query didn't select 'id' explicitly in object?
		// SurrealDB returns 'id' by default for records. Let's hope json unmarshal catches it if we add field)
		// Added ID to tempCommit.

		impact := 0.0
		if val, ok := impactMap[tc.ID]; ok {
			impact = val
		}

		c := CommitSummary{
			ID:      tc.ID,
			Hash:    tc.Hash,
			Message: tc.Message,
			Date:    tc.Date,
			Author:  authorName,
			Impact:  impact,
		}
		graphCtx.Commits = append(graphCtx.Commits, c)

		for _, iss := range tc.Issues {
			idStr := fmt.Sprintf("%v", iss.ID)

			w := 1.0
			if len(iss.Weight) > 0 {
				w = iss.Weight[0]
			}

			is := IssueSummary{
				ID:          idStr,
				Subject:     iss.Subject,
				Status:      iss.Status,
				UsageWeight: w,
			}
			issueMap[idStr] = is
		}
	}

	for _, is := range issueMap {
		graphCtx.Issues = append(graphCtx.Issues, is)
	}

	if len(graphCtx.Commits) > 5 {
		graphCtx.Commits = graphCtx.Commits[:5]
	}

	return graphCtx, nil
}
