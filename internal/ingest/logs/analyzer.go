package logs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/ai"
	"github.com/deckonline/knowledge_mcp/internal/config"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

type LogAnalyzer struct {
	Path        string
	Cfg         *config.Config
	DB          db.Executor
	AI          *ai.Client
	Project     string
	LogFileID   string
	CostTracker *ai.CostTracker
}

func NewLogAnalyzer(ctx context.Context, path string, cfg *config.Config, dbClient db.Executor, aiClient *ai.Client) *LogAnalyzer {
	base := filepath.Base(path)

	// Determine the project name from the file name (e.g., "ProjectA_server.log").
	project := "Unknown"
	if parts := strings.Split(base, "_"); len(parts) > 0 {
		project = parts[0]
	}

	// Initialize database records for the log file and its associated project.
	logFileID := db.FormatRecordID(schema.TableLogFile, db.SanitizeID(base))
	projectID := db.FormatRecordID(schema.TableProject, db.SanitizeID(project))

	if dbClient != nil {
		dbClient.Execute(ctx, fmt.Sprintf("UPDATE %s SET name = '%s';", projectID, db.EscapeSQL(project)))
		upsertQL := fmt.Sprintf("INSERT INTO %s (id, path, project, tokens_used, offset) VALUES ('%s', '%s', %s, 0, 0) "+
			"ON DUPLICATE KEY UPDATE path = '%s', project = %s;",
			schema.TableLogFile, logFileID, db.EscapeSQL(path), projectID, db.EscapeSQL(path), projectID)
		dbClient.Execute(ctx, upsertQL)
		dbClient.Execute(ctx, fmt.Sprintf("RELATE %s->%s->%s;", projectID, schema.EdgeHasLog, logFileID))
	}

	return &LogAnalyzer{
		Path:        path,
		Cfg:         cfg,
		DB:          dbClient,
		AI:          aiClient,
		Project:     project,
		LogFileID:   logFileID,
		CostTracker: ai.NewCostTracker(dbClient),
	}
}

func (a *LogAnalyzer) ProcessBatch(ctx context.Context, lines []string) {
	if len(lines) == 0 {
		return
	}
	text := strings.Join(lines, "\n")

	prompt := fmt.Sprintf(`Analyze the following log chunk from project %s. 
Extract distinct errors or anomalies. 
For each error, provide:
1. "category": a normalized, general description of the error (ignore specific IDs/timestamps)
2. "stack_trace": the relevant stack trace or context
3. "file": the source code file mentioned in the stack trace (if any)
4. "severity": a number from 1 to 10 (10 being critical)
Format your response purely as a JSON array of objects with the above keys. No markdown blocks.
Log text:
%s`, a.Project, text)

	contents := []ai.Content{{Parts: []ai.Part{{Text: prompt}}}}
	cfg := ai.GenerationConfig{Temperature: 0.1}

	if a.AI == nil || !a.AI.IsFunctional() {
		return
	}

	candidate, err := a.AI.GenerateContent(ctx, contents, cfg)
	if err != nil {
		logger.Error("AI Generation error for %s: %v", a.Path, err)
		return
	}

	if candidate.UsageMetadata != nil {
		a.CostTracker.RecordUsage(ctx, a.LogFileID, candidate.UsageMetadata.PromptTokenCount, candidate.UsageMetadata.CandidatesTokenCount)
	}

	type SemanticError struct {
		Category   string `json:"category"`
		StackTrace string `json:"stack_trace"`
		File       string `json:"file"`
		Severity   int    `json:"severity"`
	}
	var errors []SemanticError

	cleanJSON := strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(candidate.Content.Parts[0].Text), "```"), "```json")
	if err := json.Unmarshal([]byte(cleanJSON), &errors); err != nil {
		logger.Debug("Failed to parse AI JSON for %s: %v", a.Path, err)
		return
	}

	if len(errors) == 0 {
		return
	}
	logger.Info("Detected %d semantic errors in %s", len(errors), a.Path)

	for _, e := range errors {
		a.ingestError(ctx, e.Category, e.StackTrace, e.File, e.Severity)
	}
}

func (a *LogAnalyzer) ingestError(ctx context.Context, category, stack, file string, severity int) {
	if a.DB == nil {
		return
	}

	h := sha256.New()
	msg := category + stack
	if msg == "" {
		return
	}
	h.Write([]byte(msg))
	hash := hex.EncodeToString(h.Sum(nil))

	errorTypeID := db.FormatRecordID(schema.TableErrorType, hash)
	logEntryID := db.FormatRecordID(schema.TableLogEntry, fmt.Sprintf("%s_%d", hash, time.Now().UnixNano()))


	_, err := a.DB.Execute(ctx, fmt.Sprintf("UPDATE %s SET hash = '%s', category = '%s', stack_trace = '%s', severity = %d;",
		errorTypeID, hash, db.EscapeSQL(category), db.EscapeSQL(stack), severity))
	if err != nil {
		logger.Error("Failed to upsert ErrorType: %v", err)
		return
	}

	a.DB.Execute(ctx, fmt.Sprintf("RELATE %s->%s->%s;", logEntryID, schema.EdgeIsTypeOf, errorTypeID))

	embedding, err := a.AI.EmbedText(ctx, category+"\n"+stack)
	if err == nil && len(embedding) > 0 {
		var arr []string
		for _, f := range embedding {
			arr = append(arr, fmt.Sprintf("%f", f))
		}
		vecStr := "[" + strings.Join(arr, ",") + "]"
		a.DB.Execute(ctx, fmt.Sprintf("UPDATE %s SET embedding = %s;", errorTypeID, vecStr))
	}

	if file != "" {
		fileID := db.FormatRecordID(schema.TableFile, db.SanitizeID(file))
		a.DB.Execute(ctx, fmt.Sprintf("RELATE %s->%s->%s;", errorTypeID, schema.EdgeRelatedTo, fileID))
	}
}
func (a *LogAnalyzer) UpdateOffset(ctx context.Context, offset int64) {
	if a.DB == nil {
		return
	}
	a.DB.Execute(ctx, fmt.Sprintf("UPDATE %s SET offset = %d;", a.LogFileID, offset))
}

func (a *LogAnalyzer) GetOffset(ctx context.Context) int64 {
	if a.DB == nil {
		return 0
	}
	res, err := a.DB.Execute(ctx, fmt.Sprintf("SELECT offset FROM %s;", a.LogFileID))
	if err != nil {
		return 0
	}
	rows, ok := res.([]interface{})
	if !ok || len(rows) == 0 {
		return 0
	}
	row, ok := rows[0].(map[string]interface{})
	if !ok {
		return 0
	}
	offset, _ := row["offset"].(float64)
	return int64(offset)
}
