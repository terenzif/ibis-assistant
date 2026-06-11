package schema

import (
	"fmt"
	"strings"
)

const (
	// Nodes
	TableRepo        = "repo"
	TableBranch      = "branch"
	TableCommit      = "commit"
	TableAuthor      = "author"
	TableFile        = "source_file"
	TableFileChunk   = "file_chunk"
	TableCommitChunk = "commit_chunk"
	TableIssue       = "issue"        // Unified ticketing issue (Redmine/Jira/Azure DevOps)
	TableTracker     = "tracker"      // Ticket type/tracker grouping
	TableBatchJob    = "batch_job"    // Gemini Async Batch Job
	TableProject     = "project"      // Mappa dei progetti
	TableLogFile     = "log_file"     // Traccia del file monitorato
	TableLogEntry    = "log_entry"    // Singola riga/anomalia loggata
	TableErrorType   = "error_type"   // Signature log errore
	TableErrorState  = "error_state"  // Stato e temporalità dell'errore
	TableKeyUsage    = "key_usage"    // Traccia costi Gemini
	TableMemory      = "memory"       // Memoria collaborativa fornita dal client
	TableReasoning   = "reasoning"    // Esiti di ragionamento semantico
	TableLogTemplate = "log_template" // Estratti statici dei log dal codice sorgente
	TableSystem      = "system"       // Metadata di sistema e usage tracking
	TableGitCredential = "git_credential" // Credenziali git persistite
	TableLogProcessedFile     = "log_processed_file"     // Hash dei file di log già elaborati (polling)
	TableLogPendingNotification = "log_pending_notification" // Coda di notifiche per il throttling e-mail

	// Axon-like Nodes
	TableSymbol    = "symbol"    // Explicit representation of Function, Class, Method
	TableCommunity = "community" // Functional cluster of symbols
	TableProcess   = "process"   // Traced execution flow

	// Edges
	EdgeContains       = "contains"         // Repo -> Branch, Repo -> File, File -> Symbol
	EdgeParentOf       = "parent_of"        // Commit -> Commit
	EdgePointedTo      = "pointed_to"       // Branch -> Commit
	EdgeChanged        = "changed"          // Commit -> File
	EdgeAuthored       = "authored"         // Author -> Commit
	EdgeImplements     = "implements"       // Commit -> Issue
	EdgePartOf         = "part_of"          // Issue -> Tracker
	EdgeHasRepo        = "has_repo"         // Project -> Repo
	EdgeHasLog         = "has_log"          // Project -> LogFile
	EdgeHasEntry       = "has_entry"        // LogFile -> LogEntry
	EdgeIsTypeOf       = "is_type_of"       // LogEntry -> ErrorType
	EdgeRelatedTo      = "related_to"       // ErrorType -> File / FileChunk / Issue
	EdgeHasMemory      = "has_memory"       // Repo -> Memory
	EdgeHasReasoning   = "has_reasoning"    // Repo -> Reasoning
	EdgeHasCommitChunk = "has_commit_chunk" // Commit -> CommitChunk
	EdgeEmitsLog       = "emits_log"        // File -> LogTemplate

	// Axon-like Edges
	EdgeCalls       = "calls"        // Symbol -> Symbol (Function invocations)
	EdgeUsesType    = "uses_type"    // Symbol -> Symbol (Parameter/Return types)
	EdgeExtends     = "extends"      // Symbol -> Symbol (Inheritance/Interfaces)
	EdgeCoupledWith = "coupled_with" // File -> File (Co-occurring changes in Git)
	EdgeMemberOf    = "member_of"    // Symbol -> Community
)

var Definition = []string{
	fmt.Sprintf("REMOVE INDEX file_path ON TABLE %s;", TableFile),
	// Define Tables (Required for SurrealDB v3 strictness)
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableRepo),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableBranch),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableCommit),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableAuthor),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableFile),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableFileChunk),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableCommitChunk),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableIssue),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableTracker),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableBatchJob),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableProject),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableLogFile),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableLogEntry),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableErrorType),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableErrorState),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableKeyUsage),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableMemory),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableReasoning),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableLogTemplate),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableSystem),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableGitCredential),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableLogProcessedFile),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableLogPendingNotification),

	// Axon-like Tables
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableSymbol),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableCommunity),
	fmt.Sprintf("DEFINE TABLE %s SCHEMALESS;", TableProcess),

	// Define Edges (Tables for Relations)
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION SCHEMALESS;", EdgeContains),
	fmt.Sprintf("REMOVE FIELD in ON TABLE %s;", EdgeContains),
	fmt.Sprintf("REMOVE FIELD out ON TABLE %s;", EdgeContains),
	fmt.Sprintf("DEFINE FIELD in ON TABLE %s TYPE record<%s>;", EdgeContains, TableRepo),
	fmt.Sprintf("DEFINE FIELD out ON TABLE %s TYPE record<%s | %s | %s>;", EdgeContains, TableBranch, TableFile, TableSymbol),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeParentOf, TableCommit, TableCommit),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgePointedTo, TableBranch, TableCommit),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeChanged, TableCommit, TableFile),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION SCHEMALESS;", EdgeAuthored),
	fmt.Sprintf("REMOVE FIELD in ON TABLE %s;", EdgeAuthored),
	fmt.Sprintf("REMOVE FIELD out ON TABLE %s;", EdgeAuthored),
	fmt.Sprintf("DEFINE FIELD in ON TABLE %s TYPE record<%s>;", EdgeAuthored, TableAuthor),
	fmt.Sprintf("DEFINE FIELD out ON TABLE %s TYPE record<%s | %s>;", EdgeAuthored, TableCommit, TableIssue),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeImplements, TableCommit, TableIssue),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgePartOf, TableIssue, TableTracker),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeHasRepo, TableProject, TableRepo),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeHasLog, TableProject, TableLogFile),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeHasEntry, TableLogFile, TableLogEntry),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeIsTypeOf, TableLogEntry, TableErrorType),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION SCHEMALESS;", EdgeRelatedTo),
	fmt.Sprintf("REMOVE FIELD in ON TABLE %s;", EdgeRelatedTo),
	fmt.Sprintf("REMOVE FIELD out ON TABLE %s;", EdgeRelatedTo),
	fmt.Sprintf("DEFINE FIELD in ON TABLE %s TYPE record<%s>;", EdgeRelatedTo, TableErrorType),
	fmt.Sprintf("DEFINE FIELD out ON TABLE %s TYPE record<%s | %s | %s>;", EdgeRelatedTo, TableFile, TableFileChunk, TableIssue),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeHasMemory, TableRepo, TableMemory),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeHasReasoning, TableRepo, TableReasoning),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeHasCommitChunk, TableCommit, TableCommitChunk),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeEmitsLog, TableFile, TableLogTemplate),

	// Axon-like Edges definitions
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeCalls, TableSymbol, TableSymbol),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeUsesType, TableSymbol, TableSymbol),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeExtends, TableSymbol, TableSymbol),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeCoupledWith, TableFile, TableFile),
	fmt.Sprintf("DEFINE TABLE %s TYPE RELATION IN %s OUT %s SCHEMALESS;", EdgeMemberOf, TableSymbol, TableCommunity),

	// Define Indexes
	fmt.Sprintf("DEFINE INDEX commit_hash ON TABLE %s COLUMNS hash UNIQUE;", TableCommit),
	fmt.Sprintf("DEFINE INDEX file_path ON TABLE %s COLUMNS path;", TableFile),
	fmt.Sprintf("DEFINE INDEX file_version ON TABLE %s COLUMNS path, hash UNIQUE;", TableFile),
	fmt.Sprintf("REMOVE INDEX issue_id ON TABLE %s;", TableIssue),
	fmt.Sprintf("DEFINE INDEX issue_external ON TABLE %s COLUMNS provider, external_key UNIQUE;", TableIssue),

	// Log & Project Indexes
	fmt.Sprintf("DEFINE INDEX project_name ON TABLE %s COLUMNS name UNIQUE;", TableProject),
	fmt.Sprintf("DEFINE INDEX log_file_path ON TABLE %s COLUMNS path UNIQUE;", TableLogFile),
	fmt.Sprintf("DEFINE INDEX errortype_hash ON TABLE %s COLUMNS hash UNIQUE;", TableErrorType),
	fmt.Sprintf("DEFINE INDEX credential_target ON TABLE %s COLUMNS target UNIQUE;", TableGitCredential),
	fmt.Sprintf("DEFINE INDEX processed_file_hash ON TABLE %s COLUMNS hash UNIQUE;", TableLogProcessedFile),

	// Batch Job Indexes
	fmt.Sprintf("DEFINE INDEX job_status ON TABLE %s COLUMNS status;", TableBatchJob),

	// Chunk Indexes for Async Workflow
	fmt.Sprintf("DEFINE INDEX chunk_batch_id ON TABLE %s COLUMNS batch_id;", TableFileChunk),
	fmt.Sprintf("DEFINE INDEX chunk_batch_status ON TABLE %s COLUMNS batch_status;", TableFileChunk),

	// Vector Index
	fmt.Sprintf("DEFINE INDEX vector_embedding ON TABLE %s COLUMNS embedding HNSW DIMENSION 768 DIST COSINE;", TableFileChunk),
	fmt.Sprintf("DEFINE INDEX error_embedding ON TABLE %s COLUMNS embedding HNSW DIMENSION 768 DIST COSINE;", TableErrorType),
	fmt.Sprintf("DEFINE INDEX memory_embedding ON TABLE %s COLUMNS embedding HNSW DIMENSION 768 DIST COSINE;", TableMemory),
	fmt.Sprintf("DEFINE INDEX reasoning_embedding ON TABLE %s COLUMNS embedding HNSW DIMENSION 768 DIST COSINE;", TableReasoning),
	fmt.Sprintf("DEFINE INDEX commit_chunk_embedding ON TABLE %s COLUMNS embedding HNSW DIMENSION 768 DIST COSINE;", TableCommitChunk),
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
