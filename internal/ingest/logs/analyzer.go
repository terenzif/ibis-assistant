package logs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/terenzif/ibis-server/internal/ai"
	"github.com/terenzif/ibis-server/internal/config"
	"github.com/terenzif/ibis-server/internal/db"
	"github.com/terenzif/ibis-server/internal/logger"
	"github.com/terenzif/ibis-server/internal/schema"
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
	// astRuleGenUsed limits Path-D AI→ast-grep rule synthesis to once per ProcessBatchSync.
	astRuleGenUsed bool
}

func NewLogAnalyzer(ctx context.Context, path string, cfg *config.Config, dbClient db.Executor, aiClient *ai.Client) *LogAnalyzer {
	return NewLogAnalyzerWithProject(ctx, path, "", cfg, dbClient, aiClient)
}

// NewLogAnalyzerWithProject builds an analyzer bound to an explicit project name.
// When project is empty, it prefers the parent directory of path (logs/<project>/<file>)
// and falls back to the legacy "<project>_<rest>.log" filename convention.
func NewLogAnalyzerWithProject(ctx context.Context, path, project string, cfg *config.Config, dbClient db.Executor, aiClient *ai.Client) *LogAnalyzer {
	base := filepath.Base(path)

	if project == "" {
		parent := filepath.Base(filepath.Dir(filepath.Clean(path)))
		logsRootBase := ""
		if cfg != nil && cfg.LogsRoot != "" {
			logsRootBase = filepath.Base(filepath.Clean(cfg.LogsRoot))
		}
		if parent != "" && parent != "." && parent != string(filepath.Separator) && parent != logsRootBase {
			project = parent
		} else if parts := strings.Split(base, "_"); len(parts) > 1 && parts[0] != "" {
			// Legacy: "ProjectA_server.log" → ProjectA
			project = parts[0]
		} else {
			project = "Unknown"
		}
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

		// Load Static Log Templates (prefer repo-relative path from emits_log edge when present)
		tmplRes, tmplErr := dbClient.Execute(ctx, fmt.Sprintf(`
			SELECT id, format_string, regex, source_file, source_line,
				array::first(<-%s<-%s.rel_path) as edge_rel_path
			FROM %s;`, schema.EdgeEmitsLog, schema.TableFile, schema.TableLogTemplate))
		if tmplErr != nil {
			// Fallback without graph edge for older DBs
			tmplRes, tmplErr = dbClient.Execute(ctx, fmt.Sprintf(
				"SELECT id, format_string, regex, source_file, source_line FROM %s;", schema.TableLogTemplate))
		}
		if tmplErr == nil {
			if rows, ok := tmplRes.([]interface{}); ok {
				for _, r := range rows {
					if row, ok := r.(map[string]interface{}); ok {
						id := db.CoerceRecordID(row["id"])
						formatStr, _ := row["format_string"].(string)
						regexStr, _ := row["regex"].(string)
						srcFile, _ := row["source_file"].(string)
						if srcFile == "" {
							srcFile, _ = row["edge_rel_path"].(string)
						}
						srcFile = normalizeRelPath(srcFile)
						var srcLine int
						switch val := row["source_line"].(type) {
						case float64:
							srcLine = int(val)
						case int:
							srcLine = val
						case int64:
							srcLine = int(val)
						}

						if regexStr != "" {
							if !isUsefulStaticPattern(formatStr, regexStr) {
								continue
							}
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
	Line       int      `json:"line,omitempty"`
	Symbol     string   `json:"symbol,omitempty"`
	Cause      string   `json:"cause,omitempty"`
	Baseline   string   `json:"baseline,omitempty"`
	Severity   int      `json:"severity"`
	Template   string   `json:"template"`
	Supersedes []string `json:"supersedes"`
}

// Batch processing limits. Large pastes are split so each AI prompt stays bounded;
// knownPatterns accumulate across chunks within a single ProcessBatchSync call.
const (
	defaultBatchChunkSize = 250
	// MaxLogIngestBytes caps HTTP/polling body reads (~32 MiB). Prefer truncating
	// and chunk-processing over OOM; callers should surface a clear warning.
	MaxLogIngestBytes = 32 << 20
)

var (
	// knownErrorMarkerRE matches markers injected by FilterKnownErrors.
	knownErrorMarkerRE = regexp.MustCompile(`\[KNOWN_ERROR:\s*([^\]]+)\]`)
	// noiseLevelRE: log-level tokens that usually indicate non-error chatter.
	noiseLevelRE = regexp.MustCompile(`(?i)(?:^|[\[\s|/])(INFO|DEBUG|TRACE|VERBOSE)(?:[\]\s:/=]|$)`)
	// signalLevelRE: keep the line if any of these appear (conservative).
	signalLevelRE = regexp.MustCompile(`(?i)(?:ERROR|ERR\b|WARN(?:ING)?|FATAL|PANIC|EXCEPTION|CRITICAL|SEVERE|FAIL(?:URE|ED)?)`)
)

// PrefilterNoiseLines drops obvious non-error noise before AI.
//
// Heuristic (conservative — prefer keeping a line when unsure):
//   - Drop only when the line has an INFO/DEBUG/TRACE/VERBOSE level token AND
//     does not contain ERROR/WARN/FATAL/panic/exception/critical/fail signals.
//   - Stack frames, plain messages without a level, and mixed "INFO … exception"
//     lines are kept.
//
// Returns kept lines and the count of dropped noise lines.
func PrefilterNoiseLines(lines []string) (kept []string, dropped int) {
	kept = make([]string, 0, len(lines))
	for _, line := range lines {
		if isObviousNoiseLine(line) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	return kept, dropped
}

func isObviousNoiseLine(line string) bool {
	if strings.TrimSpace(line) == "" {
		return true
	}
	if signalLevelRE.MatchString(line) {
		return false
	}
	return noiseLevelRE.MatchString(line)
}

// isKnownMarkersOnly reports whether text is only [KNOWN_ERROR:…] markers and whitespace.
func isKnownMarkersOnly(text string) bool {
	remaining := knownErrorMarkerRE.ReplaceAllString(text, "")
	return strings.TrimSpace(remaining) == ""
}

// countKnownErrorMarkers returns occurrence counts keyed by category name.
func countKnownErrorMarkers(text string) map[string]int {
	matches := knownErrorMarkerRE.FindAllStringSubmatch(text, -1)
	counts := make(map[string]int, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		cat := strings.TrimSpace(m[1])
		if cat == "" {
			continue
		}
		counts[cat]++
	}
	return counts
}

// aggregateKnownMarkers builds report rows from marker counts without calling AI.
func aggregateKnownMarkers(counts map[string]int) []SemanticError {
	if len(counts) == 0 {
		return nil
	}
	out := make([]SemanticError, 0, len(counts))
	for cat, n := range counts {
		out = append(out, SemanticError{
			Category:   cat,
			StackTrace: fmt.Sprintf("[KNOWN_ERROR: %s] x%d", cat, n),
			Severity:   5,
			Cause:      fmt.Sprintf("Aggregated %d known occurrences; AI skipped (known markers only).", n),
		})
	}
	return out
}

// ReadCapped reads up to maxBytes from r. If the stream continues past maxBytes,
// truncated is true and the returned slice holds only the capped prefix.
func ReadCapped(r io.Reader, maxBytes int64) (data []byte, truncated bool, err error) {
	if maxBytes <= 0 {
		maxBytes = MaxLogIngestBytes
	}
	limited := io.LimitReader(r, maxBytes+1)
	data, err = io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > maxBytes {
		return data[:maxBytes], true, nil
	}
	return data, false, nil
}

func (a *LogAnalyzer) ProcessBatch(ctx context.Context, lines []string) {
	_, _ = a.ProcessBatchSync(ctx, lines)
}

func (a *LogAnalyzer) ProcessBatchSync(ctx context.Context, lines []string) ([]SemanticError, error) {
	if len(lines) == 0 {
		return nil, nil
	}
	a.astRuleGenUsed = false

	lines, _ = PrefilterNoiseLines(lines)
	if len(lines) == 0 {
		return nil, nil
	}

	chunkSize := defaultBatchChunkSize
	if chunkSize <= 0 {
		chunkSize = 250
	}
	nChunks := (len(lines) + chunkSize - 1) / chunkSize
	if nChunks <= 1 {
		return a.processBatchChunk(ctx, lines, 1, 1)
	}

	var all []SemanticError
	var firstErr error
	for i := 0; i < len(lines); i += chunkSize {
		end := i + chunkSize
		if end > len(lines) {
			end = len(lines)
		}
		chunkIdx := i/chunkSize + 1
		part, err := a.processBatchChunk(ctx, lines[i:end], chunkIdx, nChunks)
		if len(part) > 0 {
			all = append(all, part...)
		}
		if err != nil && firstErr == nil {
			firstErr = err
			// Continue remaining chunks so known-pattern accumulation still helps later slices.
		}
	}
	return all, firstErr
}

// processBatchChunk runs LogAlign → known-filter → optional AI for one line slice.
func (a *LogAnalyzer) processBatchChunk(ctx context.Context, lines []string, chunkIndex, chunkTotal int) ([]SemanticError, error) {
	if len(lines) == 0 {
		return nil, nil
	}

	var results []SemanticError

	// Phase 1: Deterministic LogAlign Matching (AST/static templates — bypasses AI)
	// Prefer longer format strings when multiple patterns match (avoids weak leftovers).
	var unmatchedLines []string
	for _, line := range lines {
		matched := false
		bestIdx := -1
		bestLen := -1
		for i, sp := range a.staticPatterns {
			if sp.Regex == nil || !sp.Regex.MatchString(line) {
				continue
			}
			n := len(sp.FormatStr)
			if n > bestLen {
				bestLen = n
				bestIdx = i
			}
		}
		if bestIdx >= 0 {
			sp := a.staticPatterns[bestIdx]
			se := SemanticError{
				Category:   "Static Log: " + sp.FormatStr,
				StackTrace: line,
				File:       sp.SourceFile,
				Line:       sp.SourceLine,
				Severity:   5,
				Template:   sp.FormatStr,
				Cause: fmt.Sprintf("Matched LogAlign template from %s:%d.",
					displayOr(sp.SourceFile, "unknown"), sp.SourceLine),
			}
			a.ingestError(ctx, se.Category, se.StackTrace, se.File, se.Severity, se.Template, nil)
			results = append(results, se)
			matched = true
		}
		if !matched {
			unmatchedLines = append(unmatchedLines, line)
		}
	}

	if len(unmatchedLines) == 0 {
		results = a.enrichAnomalies(ctx, results)
		return results, nil
	}

	// Phase 2: known-pattern filter, then AI only if novel text remains
	text := strings.Join(unmatchedLines, "\n")
	text = a.FilterKnownErrors(text)
	markerCounts := countKnownErrorMarkers(text)

	if isKnownMarkersOnly(text) {
		aggregated := aggregateKnownMarkers(markerCounts)
		totalHits := 0
		for _, n := range markerCounts {
			totalHits += n
		}
		if len(results) > 0 {
			results = a.enrichAnomalies(ctx, results)
		}
		results = append(results, aggregated...)
		return results, nil
	}

	var activeCategories []string
	for _, p := range a.knownPatterns {
		activeCategories = append(activeCategories, p.Category)
	}

	baseline := a.resolveBaseline(ctx)
	repoHints := a.gatherRepoHints(ctx, unmatchedLines)

	prompt := fmt.Sprintf(`Analyze the following log chunk from project %s.
Code baseline for correlation: %s.
Extract distinct errors or anomalies — classify AND pinpoint likely source location / root cause using repo context when available.
Currently known error categories for this project: [%s]. Use this context to build incremental knowledge.
Repo knowledge hints (may be empty):
%s

For each error, provide:
1. "category": a normalized, general description of the error (ignore specific IDs/timestamps)
2. "stack_trace": the relevant stack trace or context
3. "file": repo-relative source path when known (from stack frames OR repo hints). Prefer paths like internal/db/client.go over bare basenames. Empty string only if truly unknown.
4. "line": integer source line when known, else 0
5. "symbol": function/method/symbol name when known, else ""
6. "cause": one short sentence explaining the likely root cause grounded in the log + repo hints (not just restating the category)
7. "severity": a number from 1 to 10 (10 being critical)
8. "template": The exact raw text of the entire error block (including cascade logs), but replace any dynamic/variable parts (like timestamps, specific IDs, memory addresses) with the literal string "<VAR>". Do not use regex.
9. "supersedes": a string array of known categories that this new error incorporates or replaces (e.g., if this is a cascade that contains them).
Note: Previously known errors have been replaced with markers like [KNOWN_ERROR: category]. If you see these markers, understand that the corresponding error occurred there.
Format your response purely as a JSON array of objects with the above keys. No markdown blocks.
Log text:
%s`, a.Project, baseline.String(), strings.Join(activeCategories, ", "),
		displayOr(repoHints, "(none)"), text)

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

	if candidate.UsageMetadata != nil && a.CostTracker != nil {
		a.CostTracker.RecordUsage(ctx, a.LogFileID, candidate.UsageMetadata.PromptTokenCount, candidate.UsageMetadata.CandidatesTokenCount)
	}

	var errors []SemanticError
	cleanJSON := strings.TrimSpace(candidate.Content.Parts[0].Text)
	rawPreview := cleanJSON
	if len(rawPreview) > 180 {
		rawPreview = rawPreview[:180]
	}
	if strings.HasPrefix(cleanJSON, "```") {
		if idx := strings.Index(cleanJSON, "\n"); idx != -1 {
			cleanJSON = cleanJSON[idx+1:]
		}
	}
	if strings.HasSuffix(strings.TrimRight(cleanJSON, " \t\r\n"), "```") {
		cleanJSON = strings.TrimRight(cleanJSON, " \t\r\n")
		cleanJSON = cleanJSON[:len(cleanJSON)-3]
	}
	cleanJSON = strings.TrimSpace(cleanJSON)
	if err := json.Unmarshal([]byte(cleanJSON), &errors); err != nil {
		logger.Debug("Failed to parse AI JSON for %s: %v", a.Path, err)
		return results, fmt.Errorf("failed to parse AI JSON: %w", err)
	}

	if len(errors) == 0 {
		if len(results) > 0 {
			results = a.enrichAnomalies(ctx, results)
		}
		return results, nil
	}
	logger.Info("Detected %d semantic errors in %s (chunk %d/%d)", len(errors), a.Path, chunkIndex, chunkTotal)

	errors = a.enrichAnomalies(ctx, errors)

	// Path C: promote AI templates into durable log_template / staticPatterns
	// so subsequent lines/chunks LogAlign-bypass classify AI (AST remains primary).
	_ = a.PromoteAIErrorsToLogAlign(ctx, errors)

	for _, e := range errors {
		if e.Template != "" {
			a.AddKnownPattern(e.Category, e.Template)
		}
		a.ingestError(ctx, e.Category, e.StackTrace, e.File, e.Severity, e.Template, e.Supersedes)
		results = append(results, e)
	}

	if nStatic := len(results) - len(errors); nStatic > 0 {
		staticPart := a.enrichAnomalies(ctx, results[:nStatic])
		results = append(staticPart, results[nStatic:]...)
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
	patternStr := maskTemplateToRegex(template)
	regex, err := regexp.Compile("(?s)" + patternStr)
	if err != nil {
		logger.Error("Failed to compile regex from AI template for category %s: %v", category, err)
		return err
	}

	a.knownPatterns = append(a.knownPatterns, Pattern{Category: category, Regex: regex})
	return nil
}

// maskTemplateToRegex converts an AI <VAR> mask template into a RE2 fragment.
// Trailing <VAR> uses [^\n]* so the dynamic suffix is consumed (.*? at end
// would match empty and leave residue, defeating known-marker early-exit).
func maskTemplateToRegex(template string) string {
	parts := strings.Split(template, "<VAR>")
	var b strings.Builder
	for i, part := range parts {
		b.WriteString(regexp.QuoteMeta(part))
		if i < len(parts)-1 {
			if i == len(parts)-2 && parts[len(parts)-1] == "" {
				b.WriteString(`[^\n]*`)
			} else {
				b.WriteString(`.*?`)
			}
		}
	}
	return b.String()
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
