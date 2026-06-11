package logs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/terenzif/ibis-arc/internal/ai"
	"github.com/terenzif/ibis-arc/internal/config"
	"github.com/terenzif/ibis-arc/internal/db"
	"github.com/terenzif/ibis-arc/internal/logger"
	"github.com/terenzif/ibis-arc/internal/schema"
)

type Pattern struct {
	Category string
	Regex    *regexp.Regexp
}

type LogTemplatePattern struct {
	ID         string
	Regex      *regexp.Regexp
	FormatStr  string
	SourceFile string
	SourceLine int
}

type LogAnalyzer struct {
	Path           string
	Cfg            *config.Config
	DB             db.Executor
	AI             *ai.Client
	Project        string
	LogFileID      string
	CostTracker    *ai.CostTracker
	knownPatterns  []Pattern
	staticPatterns []LogTemplatePattern
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
		upsertQL := fmt.Sprintf("UPDATE %s MERGE { path: '%s', project: %s };",
			logFileID, db.EscapeSQL(path), projectID)
		dbClient.Execute(ctx, upsertQL)
		dbClient.Execute(ctx, fmt.Sprintf("RELATE %s->%s->%s;", projectID, schema.EdgeHasLog, logFileID))
	}

	analyzer := &LogAnalyzer{
		Path:          path,
		Cfg:           cfg,
		DB:            dbClient,
		AI:            aiClient,
		Project:       project,
		LogFileID:     logFileID,
		CostTracker:   ai.NewCostTracker(dbClient),
		knownPatterns: make([]Pattern, 0),
	}

	if dbClient != nil {
		res, err := dbClient.Execute(ctx, fmt.Sprintf("SELECT category, template, created_at FROM %s WHERE template != '' AND hidden != true ORDER BY created_at ASC;", schema.TableErrorType))
		if err == nil {
			if rows, ok := res.([]interface{}); ok {
				for _, r := range rows {
					if row, ok := r.(map[string]interface{}); ok {
						cat, _ := row["category"].(string)
						tmpl, _ := row["template"].(string)
						if cat != "" && tmpl != "" {
							analyzer.AddKnownPattern(cat, tmpl)
						}
					}
				}
			}
		}

		// Load Static Log Templates
		tmplRes, tmplErr := dbClient.Execute(ctx, fmt.Sprintf("SELECT id, format_string, regex, source_file, source_line FROM %s;", schema.TableLogTemplate))
		if tmplErr == nil {
			if rows, ok := tmplRes.([]interface{}); ok {
				for _, r := range rows {
					if row, ok := r.(map[string]interface{}); ok {
						id, _ := row["id"].(string)
						formatStr, _ := row["format_string"].(string)
						regexStr, _ := row["regex"].(string)
						srcFile, _ := row["source_file"].(string)
						var srcLine int
						if val, ok := row["source_line"].(float64); ok {
							srcLine = int(val)
						}

						if regexStr != "" {
							re, err := regexp.Compile("(?s)" + regexStr)
							if err == nil {
								analyzer.staticPatterns = append(analyzer.staticPatterns, LogTemplatePattern{
									ID:         id,
									Regex:      re,
									FormatStr:  formatStr,
									SourceFile: srcFile,
									SourceLine: srcLine,
								})
							}
						}
					}
				}
			}
		}
	}

	return analyzer
}

type SemanticError struct {
	Category   string   `json:"category"`
	StackTrace string   `json:"stack_trace"`
	File       string   `json:"file"`
	Severity   int      `json:"severity"`
	Template   string   `json:"template"`
	Supersedes []string `json:"supersedes"`
}

func (a *LogAnalyzer) ProcessBatch(ctx context.Context, lines []string) {
	_, _ = a.ProcessBatchSync(ctx, lines)
}

func (a *LogAnalyzer) ProcessBatchSync(ctx context.Context, lines []string) ([]SemanticError, error) {
	if len(lines) == 0 {
		return nil, nil
	}

	var results []SemanticError

	// Phase 1: Deterministic LogAlign Matching
	var unmatchedLines []string
	for _, line := range lines {
		matched := false
		for _, sp := range a.staticPatterns {
			if sp.Regex.MatchString(line) {
				// Deterministic match found! No AI needed.
				a.ingestError(ctx, "Static Log: "+sp.FormatStr, line, sp.SourceFile, 5, sp.FormatStr, nil)
				results = append(results, SemanticError{
					Category:   "Static Log: " + sp.FormatStr,
					StackTrace: line,
					File:       sp.SourceFile,
					Severity:   5,
					Template:   sp.FormatStr,
				})
				matched = true
				break
			}
		}
		if !matched {
			unmatchedLines = append(unmatchedLines, line)
		}
	}

	if len(unmatchedLines) == 0 {
		return results, nil // Everything was deterministically matched
	}

	// Phase 2: AI Processing for unmatched lines
	text := strings.Join(unmatchedLines, "\n")
	text = a.FilterKnownErrors(text)

	var activeCategories []string
	for _, p := range a.knownPatterns {
		activeCategories = append(activeCategories, p.Category)
	}

	prompt := fmt.Sprintf(`Analyze the following log chunk from project %s. 
Extract distinct errors or anomalies. 
Currently known error categories for this project: [%s]. Use this context to build incremental knowledge.
For each error, provide:
1. "category": a normalized, general description of the error (ignore specific IDs/timestamps)
2. "stack_trace": the relevant stack trace or context
3. "file": the source code file mentioned in the stack trace (if any)
4. "severity": a number from 1 to 10 (10 being critical)
5. "template": The exact raw text of the entire error block (including cascade logs), but replace any dynamic/variable parts (like timestamps, specific IDs, memory addresses) with the literal string "<VAR>". Do not use regex.
6. "supersedes": a string array of known categories that this new error incorporates or replaces (e.g., if this is a cascade that contains them).
Note: Previously known errors have been replaced with markers like [KNOWN_ERROR: category]. If you see these markers, understand that the corresponding error occurred there.
Format your response purely as a JSON array of objects with the above keys. No markdown blocks.
Log text:
%s`, a.Project, strings.Join(activeCategories, ", "), text)

	contents := []ai.Content{{Parts: []ai.Part{{Text: prompt}}}}
	cfg := ai.GenerationConfig{Temperature: 0.1}

	if a.AI == nil || !a.AI.IsFunctional() {
		return results, fmt.Errorf("AI client is not functional")
	}

	candidate, err := a.AI.GenerateContent(ctx, contents, cfg)
	if err != nil {
		logger.Error("AI Generation error for %s: %v", a.Path, err)
		return results, err
	}

	if candidate.UsageMetadata != nil {
		a.CostTracker.RecordUsage(ctx, a.LogFileID, candidate.UsageMetadata.PromptTokenCount, candidate.UsageMetadata.CandidatesTokenCount)
	}

	var errors []SemanticError
	cleanJSON := strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(candidate.Content.Parts[0].Text), "```"), "```json")
	if err := json.Unmarshal([]byte(cleanJSON), &errors); err != nil {
		logger.Debug("Failed to parse AI JSON for %s: %v", a.Path, err)
		return results, fmt.Errorf("failed to parse AI JSON: %w", err)
	}

	if len(errors) == 0 {
		return results, nil
	}
	logger.Info("Detected %d semantic errors in %s", len(errors), a.Path)

	for _, e := range errors {
		if e.Template != "" {
			a.AddKnownPattern(e.Category, e.Template)
		}
		a.ingestError(ctx, e.Category, e.StackTrace, e.File, e.Severity, e.Template, e.Supersedes)
		results = append(results, e)
	}

	return results, nil
}

func (a *LogAnalyzer) FilterKnownErrors(text string) string {
	for _, pattern := range a.knownPatterns {
		marker := fmt.Sprintf("[KNOWN_ERROR: %s]", pattern.Category)
		text = pattern.Regex.ReplaceAllString(text, marker)
	}
	return text
}

func (a *LogAnalyzer) AddKnownPattern(category, template string) error {
	escaped := regexp.QuoteMeta(template)
	patternStr := strings.ReplaceAll(escaped, "<VAR>", ".*?")

	regex, err := regexp.Compile("(?s)" + patternStr)
	if err != nil {
		logger.Error("Failed to compile regex from AI template for category %s: %v", category, err)
		return err
	}

	a.knownPatterns = append(a.knownPatterns, Pattern{Category: category, Regex: regex})
	return nil
}

func (a *LogAnalyzer) DiscoverFormat(ctx context.Context, lines []string) string {
	if a.AI == nil || !a.AI.IsFunctional() || len(lines) == 0 {
		return ""
	}

	sample := strings.Join(lines, "\n")
	prompt := fmt.Sprintf(`Analyze the following log sample.
What is the regular expression (RE2) that uniquely identifies the absolute BEGINNING of a new log entry (e.g. a timestamp or log level)?
Return ONLY the raw regex string, nothing else. If there is no clear delimiter, return an empty string.
Log sample:
%s`, sample)

	contents := []ai.Content{{Parts: []ai.Part{{Text: prompt}}}}
	cfg := ai.GenerationConfig{Temperature: 0.0}

	candidate, err := a.AI.GenerateContent(ctx, contents, cfg)
	if err != nil {
		return ""
	}

	regexStr := strings.TrimSpace(candidate.Content.Parts[0].Text)
	regexStr = strings.TrimPrefix(regexStr, "`")
	regexStr = strings.TrimSuffix(regexStr, "`")
	regexStr = strings.TrimPrefix(regexStr, "\"")
	regexStr = strings.TrimSuffix(regexStr, "\"")

	return regexStr
}

func (a *LogAnalyzer) ingestError(ctx context.Context, category, stack, file string, severity int, template string, supersedes []string) {
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

	timestamp := time.Now().Format(time.RFC3339)
	_, err := a.DB.Execute(ctx, fmt.Sprintf("UPDATE %s SET hash = '%s', category = '%s', stack_trace = '%s', severity = %d, template = '%s', created_at = '%s', hidden = false;",
		errorTypeID, hash, db.EscapeSQL(category), db.EscapeSQL(stack), severity, db.EscapeSQL(template), timestamp))
	if err != nil {
		logger.Error("Failed to upsert ErrorType: %v", err)
		return
	}

	for _, sup := range supersedes {
		a.DB.Execute(ctx, fmt.Sprintf("UPDATE %s SET hidden = true WHERE category = '%s';", schema.TableErrorType, db.EscapeSQL(sup)))
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
