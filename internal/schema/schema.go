package schema

import (
	"fmt"
	"strings"
)

const (
	// Nodes
	TableRepo      = "repo"
	TableBranch    = "branch"
	TableCommit    = "commit"
	TableAuthor    = "author"
	TableFile      = "file"
	TableFileChunk = "file_chunk"
	TableIssue     = "issue"   // Redmine Issue
	TableTracker   = "tracker" // Redmine Tracker/Epic
	TableBatchJob  = "batch_job" // Gemini Async Batch Job

	// Edges
	EdgeContains   = "contains"   // Repo -> Branch, Repo -> File
	EdgeParentOf   = "parent_of"  // Commit -> Commit
	EdgePointedTo  = "pointed_to" // Branch -> Commit
	EdgeChanged    = "changed"    // Commit -> File
	EdgeAuthored   = "authored"   // Author -> Commit
	EdgeImplements = "implements" // Commit -> Issue
	EdgePartOf     = "part_of"    // Issue -> Tracker
)

var Definition = []string{
	// Define Indexes
	fmt.Sprintf("DEFINE INDEX commit_hash ON TABLE %s COLUMNS hash UNIQUE;", TableCommit),
	fmt.Sprintf("DEFINE INDEX file_path ON TABLE %s COLUMNS path UNIQUE;", TableFile),
	fmt.Sprintf("DEFINE INDEX issue_id ON TABLE %s COLUMNS id UNIQUE;", TableIssue),

	// Batch Job Indexes
	fmt.Sprintf("DEFINE INDEX job_status ON TABLE %s COLUMNS status;", TableBatchJob),

	// Chunk Indexes for Async Workflow
	fmt.Sprintf("DEFINE INDEX chunk_batch_id ON TABLE %s COLUMNS batch_id;", TableFileChunk),
	fmt.Sprintf("DEFINE INDEX chunk_batch_status ON TABLE %s COLUMNS batch_status;", TableFileChunk),

	// Vector Index
	fmt.Sprintf("DEFINE INDEX vector_embedding ON TABLE %s COLUMNS embedding M-TREE DIMENSION 768 DIST COSINE;", TableFileChunk),
}

// GenerateInitSQL returns the full SQL script to initialize the DB
func GenerateInitSQL() string {
	var sb strings.Builder
	for _, s := range Definition {
		sb.WriteString(s)
		sb.WriteString("\n")
	}
	return sb.String()
}
