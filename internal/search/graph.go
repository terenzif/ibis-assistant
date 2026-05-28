package search

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/deckonline/knowledge_mcp/internal/schema"
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
func GetFileContext(ctx context.Context, dbClient db.Executor, filePath string) (*GraphContext, error) {
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

	res, err := dbClient.SmartQuery(ctx, ql, map[string]interface{}{
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
				if inRaw, ok := em["in"]; ok {
					// Fix: The ID from the edge (inRaw) might be a driver-specific type (e.g. RecordID struct)
					// while the ID from history is parsed via JSON unmarshal (which converts it to string).
					// To ensure they match, we should try to treat inRaw as JSON if it's not a simple string.
					var inID string
					if s, ok := inRaw.(string); ok {
						inID = s
					} else if b, err := json.Marshal(inRaw); err == nil {
						// JSON strings are quoted, remove them
						s := string(b)
						if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
							s = s[1 : len(s)-1]
						}
						inID = s
					} else {
						// Fallback
						inID = fmt.Sprintf("%v", inRaw)
					}

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
		ID      interface{} `json:"id"` // Need ID to map impact
		Hash    string      `json:"hash"`
		Message string      `json:"message"`
		Date    string `json:"date"`
		Author  []string `json:"author"`
		Issues  []struct {
			ID      interface{} `json:"id"`
			Subject string      `json:"subject"`
			Status  string      `json:"status"`
			Weight  interface{} `json:"weight"` // Can be float64 or []interface{} (depending on DB driver and result shape)
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

		idStr := fmt.Sprintf("%v", tc.ID)

		// Lookup impact using Commit ID
		impact := 0.0
		if val, ok := impactMap[idStr]; ok {
			impact = val
		}

		c := CommitSummary{
			ID:      idStr,
			Hash:    tc.Hash,
			Message: tc.Message,
			Date:    tc.Date,
			Author:  authorName,
			Impact:  impact,
		}
		graphCtx.Commits = append(graphCtx.Commits, c)

		for _, iss := range tc.Issues {
			idStr := fmt.Sprintf("%v", iss.ID)

			// Handle potentially scalar or array weight
			w := 1.0
			switch val := iss.Weight.(type) {
			case float64:
				w = val
			case []interface{}:
				// If it's an array, take the first element if it's a number
				if len(val) > 0 {
					if f, ok := val[0].(float64); ok {
						w = f
					}
				}
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

// SymbolContext represents the architectural context of a symbol
type SymbolContext struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	File          string   `json:"file"`
	Callers       []string `json:"callers"`
	CalledSymbols []string `json:"called_symbols"`
	BlastRadius   []string `json:"blast_radius"`
}

// AnalyzeBlastRadius performs a graph traversal to find all upstream functions that call this symbol
func AnalyzeBlastRadius(ctx context.Context, dbClient db.Executor, repoName, symbolFile, symbolName string) (*SymbolContext, error) {
	// Construct the symbol ID exactly as ingested
	safeRepoName := db.SanitizeID(repoName)
	symbolID := db.FormatRecordID(schema.TableSymbol, fmt.Sprintf("%s_%s_%s", safeRepoName, db.SanitizeID(symbolFile), db.SanitizeID(symbolName)))

	// Use SurrealQL Graph Traversal to go backwards through 'calls' edges
	// <-calls<-symbol gets direct callers
	// <->calls gets both directions.
	// For blast radius we want everything that depends on this symbol.
	// We can use a recursive fetch if SurrealDB supports it, but simple depth is ok.
	ql := fmt.Sprintf(`
	SELECT
		name,
		kind,
		file.path as file_path,
		<-calls<-symbol.name as direct_callers,
		->calls->symbol.name as calls_out,
		<-calls<-symbol<-calls<-symbol.name as blast_radius
	FROM %s;
	`, symbolID)

	res, err := dbClient.Execute(ctx, ql)
	if err != nil {
		return nil, fmt.Errorf("failed to query blast radius: %w", err)
	}

	rows, ok := res.([]interface{})
	if !ok || len(rows) == 0 {
		return nil, fmt.Errorf("symbol not found in graph")
	}

	row := rows[0].(map[string]interface{})

	symCtx := &SymbolContext{
		Name: safeGetString(row, "name"),
		Kind: safeGetString(row, "kind"),
		File: safeGetString(row, "file_path"),
	}

	if directCallers, ok := row["direct_callers"].([]interface{}); ok {
		for _, c := range directCallers {
			symCtx.Callers = append(symCtx.Callers, c.(string))
		}
	}

	if callsOut, ok := row["calls_out"].([]interface{}); ok {
		for _, c := range callsOut {
			symCtx.CalledSymbols = append(symCtx.CalledSymbols, c.(string))
		}
	}

	if blastRadius, ok := row["blast_radius"].([]interface{}); ok {
		for _, c := range blastRadius {
			symCtx.BlastRadius = append(symCtx.BlastRadius, c.(string))
		}
	}

	return symCtx, nil
}

// FindDeadCode finds symbols (Functions/Methods) in the repository that have no incoming call edges
func FindDeadCode(ctx context.Context, dbClient db.Executor, repoName string) ([]string, error) {
	// Query: Select symbols in this repo that are functions and have count(<-calls) == 0
	// We filter out Test functions implicitly or explicitly in production.
	ql := fmt.Sprintf(`
	SELECT name, file.path as file_path
	FROM %s
	WHERE string::starts_with(id, 'symbol:%s')
	  AND (kind = 'function_declaration' OR kind = 'method_declaration')
	  AND array::len(<-calls) = 0;
	`, schema.TableSymbol, db.SanitizeID(repoName))

	res, err := dbClient.Execute(ctx, ql)
	if err != nil {
		return nil, fmt.Errorf("failed to query dead code: %w", err)
	}

	rows, ok := res.([]interface{})
	if !ok {
		return []string{}, nil
	}

	var deadSymbols []string
	for _, raw := range rows {
		row := raw.(map[string]interface{})
		name := safeGetString(row, "name")
		file := safeGetString(row, "file_path")
		deadSymbols = append(deadSymbols, fmt.Sprintf("%s (in %s)", name, file))
	}

	return deadSymbols, nil
}

func safeGetString(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok && val != nil {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}
