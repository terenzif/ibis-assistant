package logs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/terenzif/ibis-assistant/internal/db"
	"github.com/terenzif/ibis-assistant/internal/logger"
	"github.com/terenzif/ibis-assistant/internal/schema"
)

const (
	maxLogAlignRegexLen  = 2048
	maxLogAlignFormatLen = 4000
	maxAIPromotePerBatch = 8
	minStaticLiteralRunes = 8
	minStaticFormatLen    = 12
)

// isUsefulStaticPattern rejects overly broad LogAlign rules (e.g. format "w"
// / regex "(.*?)") that would match unrelated lines and skip classify AI wrongly.
func isUsefulStaticPattern(format, regex string) bool {
	f := strings.TrimSpace(format)
	f = strings.Trim(f, "\"`'")
	if len(f) < minStaticFormatLen {
		return false
	}
	r := strings.TrimSpace(regex)
	if r == "" || r == "(.*?)" || r == "(.*)" || r == ".+" || r == ".*" {
		return false
	}
	stripped := r
	for _, tok := range []string{
		"(.*?)", "(.*)", `([-+]?\d+)`, `([-+]?\d+(?:\.\d+)?)`,
		"(true|false)", `([0-9a-fA-F]+)`, `[^\n]*`,
	} {
		stripped = strings.ReplaceAll(stripped, tok, "")
	}
	stripped = strings.ReplaceAll(stripped, `\`, "")
	literalRunes := 0
	for _, ch := range stripped {
		if ch != '(' && ch != ')' && ch != '?' && ch != '+' && ch != '*' && ch != '.' && ch != '|' {
			literalRunes++
		}
	}
	return literalRunes >= minStaticLiteralRunes
}

// templateToFlexibleRegex turns an AI <VAR> mask template into a RE2 substring
// pattern suitable for Phase-1 LogAlign (no ^/$ anchors — runtime prefixes OK).
func templateToFlexibleRegex(template string) (string, error) {
	template = strings.TrimSpace(template)
	if template == "" {
		return "", fmt.Errorf("empty template")
	}
	if utf8.RuneCountInString(template) > maxLogAlignFormatLen {
		return "", fmt.Errorf("template too long")
	}

	// Same construction as AddKnownPattern: trailing <VAR> consumes rest of line.
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
	pattern := b.String()

	if len(pattern) > maxLogAlignRegexLen {
		return "", fmt.Errorf("regex too long")
	}
	if looksDangerousRegex(pattern) {
		return "", fmt.Errorf("rejected dangerous regex shape")
	}
	return pattern, nil
}

// looksDangerousRegex applies cheap heuristics. Go's regexp is RE2 (linear), so
// classic catastrophic backtracking is not an issue; we still reject nested
// unbounded wildcards and extremely nested groups that are useless/noisy.
func looksDangerousRegex(pattern string) bool {
	lower := strings.ToLower(pattern)
	dangerous := []string{
		"(.*)*.*",
		"(.*?)*",
		"(.+)+",
		"(.*)+",
		"(a+)+",
		"(.*){,",
	}
	for _, d := range dangerous {
		if strings.Contains(lower, d) {
			return true
		}
	}
	// Too many nested groups
	if strings.Count(pattern, "(") > 40 {
		return true
	}
	return false
}

// safeCompileLogAlign compiles a LogAlign regex with (?s) and length guards.
func safeCompileLogAlign(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("empty regex")
	}
	if len(pattern) > maxLogAlignRegexLen {
		return nil, fmt.Errorf("regex too long (%d)", len(pattern))
	}
	if looksDangerousRegex(pattern) {
		return nil, fmt.Errorf("rejected dangerous regex shape")
	}
	re, err := regexp.Compile("(?s)" + pattern)
	if err != nil {
		return nil, err
	}
	return re, nil
}

// alreadyCoveredByStatic reports whether line already matches a static pattern.
func (a *LogAnalyzer) alreadyCoveredByStatic(line string) bool {
	for _, sp := range a.staticPatterns {
		if sp.Regex != nil && sp.Regex.MatchString(line) {
			return true
		}
	}
	return false
}

// PromoteAIErrorsToLogAlign synthesizes durable log_template rows from AI
// classifications that AST extract did not cover. Valid rules are appended to
// in-memory staticPatterns immediately (same-batch / next-chunk LogAlign bypass)
// and UPSERTed into SurrealDB.
func (a *LogAnalyzer) PromoteAIErrorsToLogAlign(ctx context.Context, errors []SemanticError) int {
	if a == nil || len(errors) == 0 {
		return 0
	}

	promoted := 0
	for _, e := range errors {
		if promoted >= maxAIPromotePerBatch {
			break
		}
		tmpl := strings.TrimSpace(e.Template)
		if tmpl == "" {
			continue
		}
		// Prefer matching against stack/context line when available.
		probe := e.StackTrace
		if probe == "" {
			probe = tmpl
		}
		if a.alreadyCoveredByStatic(probe) || a.alreadyCoveredByStatic(tmpl) {
			continue
		}

		regexStr, err := templateToFlexibleRegex(tmpl)
		if err != nil {
			continue
		}
		if !isUsefulStaticPattern(tmpl, regexStr) {
			continue
		}
		re, err := safeCompileLogAlign(regexStr)
		if err != nil {
			continue
		}
		// Sanity: rule should match the probe it was derived from (or the template itself).
		if !re.MatchString(probe) && !re.MatchString(tmpl) {
			continue
		}

		srcFile := normalizeRelPath(e.File)
		srcLine := e.Line
		formatStr := tmpl

		h := sha256.Sum256([]byte("ai|" + formatStr + "|" + srcFile))
		idSuffix := "ai_" + hex.EncodeToString(h[:12])
		logID := db.FormatRecordID(schema.TableLogTemplate, idSuffix)

		pat := LogTemplatePattern{
			ID:         logID,
			Regex:      re,
			FormatStr:  formatStr,
			SourceFile: srcFile,
			SourceLine: srcLine,
		}
		a.staticPatterns = append(a.staticPatterns, pat)
		a.persistLogTemplate(ctx, logID, formatStr, regexStr, srcFile, srcLine, e.Category)
		promoted++

	}

	if promoted > 0 {
		logger.Info("LogAlign: promoted %d AI template(s) → log_template (staticPatterns=%d)",
			promoted, len(a.staticPatterns))
	}
	return promoted
}

func (a *LogAnalyzer) persistLogTemplate(ctx context.Context, logID, formatStr, regexStr, srcFile string, srcLine int, category string) {
	if a.DB == nil {
		return
	}
	formatBytes, _ := json.Marshal(formatStr)
	regexBytes, _ := json.Marshal(regexStr)
	ql := fmt.Sprintf(
		`UPSERT %s SET format_string=%s, regex=%s, source_file='%s', source_line=%d, origin='ai', category='%s', updated_at=time::now();`,
		logID, string(formatBytes), string(regexBytes),
		db.EscapeSQL(srcFile), srcLine, db.EscapeSQL(category),
	)
	if _, err := a.DB.Execute(ctx, ql); err != nil {
		logger.Warn("LogAlign: failed to persist AI log_template %s: %v", logID, err)
	}
}
