package schema

import (
	"fmt"
	"strings"
)

const (
	// Nodes
	TableRepo       = "repo"
	TableBranch     = "branch"
	TableCommit     = "commit"
	TableAuthor     = "author"
	TableFile       = "file"
	TableFileChunk  = "file_chunk"
	TableIssue      = "issue"       // Redmine Issue
	TableTracker    = "tracker"     // Redmine Tracker/Epic
	TableBatchJob   = "batch_job"   // Gemini Async Batch Job
	TableProject    = "project"     // Mappa dei progetti
	TableLogFile    = "log_file"    // Traccia del file monitorato
	TableLogEntry   = "log_entry"   // Singola riga/anomalia loggata
	TableErrorType  = "error_type"  // Signature log errore
	TableErrorState = "error_state" // Stato e temporalità dell'errore

	// Edges
	EdgeContains   = "contains"   // Repo -> Branch, Repo -> File
	EdgeParentOf   = "parent_of"  // Commit -> Commit
	EdgePointedTo  = "pointed_to" // Branch -> Commit
	EdgeChanged    = "changed"    // Commit -> File
	EdgeAuthored   = "authored"   // Author -> Commit
	EdgeImplements = "implements" // Commit -> Issue
	EdgePartOf     = "part_of"    // Issue -> Tracker
	EdgeHasRepo    = "has_repo"   // Project -> Repo
	EdgeHasLog     = "has_log"    // Project -> LogFile
	EdgeHasEntry   = "has_entry"  // LogFile -> LogEntry
	EdgeIsTypeOf   = "is_type_of" // LogEntry -> ErrorType
	EdgeRelatedTo  = "related_to" // ErrorType -> File / FileChunk / Issue
)

var Definition = []string{
	// Define Indexes
	fmt.Sprintf("DEFINE INDEX commit_hash ON TABLE %s COLUMNS hash UNIQUE;", TableCommit),
	fmt.Sprintf("DEFINE INDEX file_path ON TABLE %s COLUMNS path UNIQUE;", TableFile),
	fmt.Sprintf("DEFINE INDEX issue_id ON TABLE %s COLUMNS id UNIQUE;", TableIssue),

	// Log & Project Indexes
	fmt.Sprintf("DEFINE INDEX project_name ON TABLE %s COLUMNS name UNIQUE;", TableProject),
	fmt.Sprintf("DEFINE INDEX log_file_path ON TABLE %s COLUMNS path UNIQUE;", TableLogFile),
	fmt.Sprintf("DEFINE INDEX errortype_hash ON TABLE %s COLUMNS hash UNIQUE;", TableErrorType),

	// Batch Job Indexes
	fmt.Sprintf("DEFINE INDEX job_status ON TABLE %s COLUMNS status;", TableBatchJob),

	// Chunk Indexes for Async Workflow
	fmt.Sprintf("DEFINE INDEX chunk_batch_id ON TABLE %s COLUMNS batch_id;", TableFileChunk),
	fmt.Sprintf("DEFINE INDEX chunk_batch_status ON TABLE %s COLUMNS batch_status;", TableFileChunk),

	// Vector Index
	fmt.Sprintf("DEFINE INDEX vector_embedding ON TABLE %s COLUMNS embedding M-TREE DIMENSION 768 DIST COSINE;", TableFileChunk),
	fmt.Sprintf("DEFINE INDEX error_embedding ON TABLE %s COLUMNS embedding M-TREE DIMENSION 768 DIST COSINE;", TableErrorType),
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
