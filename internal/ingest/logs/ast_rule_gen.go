package logs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/terenzif/ibis-server/internal/ai"
	"github.com/terenzif/ibis-server/internal/ingest/code"
	"github.com/terenzif/ibis-server/internal/logger"
)

const (
	maxASTRuleSampleLines = 12
	maxASTRuleYAMLBytes   = 24 << 10
	astRuleQuarantineDir  = "rules_quarantine"
)

var (
	astRuleGenMu       sync.Mutex
	astRuleGenRecent   = map[string]time.Time{} // fingerprint → last attempt
	astRuleGenCooldown = 30 * time.Minute
)

// ASTRuleGenResult is the outcome of one AI → ast-grep rule attempt.
type ASTRuleGenResult struct {
	Path       string // written rule path (empty if quarantined / skipped)
	Quarantine string // quarantine path when validation failed
	RuleID     string
	Language   string
	Validated  bool
}

type aiASTRuleResponse struct {
	Language   string `json:"language"`
	ID         string `json:"id"`
	YAML       string `json:"yaml"`
	SampleCode string `json:"sample_code"`
	Rationale  string `json:"rationale"`
}

// shouldAttemptASTRuleGen gates Path-D AST rule synthesis (cost-bounded).
// Call at most once per ProcessBatchSync when AI classified errors that likely
// come from in-repo source with a logger style not yet covered by *-logs.yml.
func (a *LogAnalyzer) shouldAttemptASTRuleGen(errors []SemanticError) bool {
	if a == nil || a.AI == nil || !a.AI.IsFunctional() || len(errors) == 0 {
		return false
	}
	if a.astRuleGenUsed {
		return false
	}
	lang := inferLanguageFromErrors(errors)
	if lang == "" {
		return false
	}
	fp := astRuleFingerprint(lang, errors)
	astRuleGenMu.Lock()
	defer astRuleGenMu.Unlock()
	if t, ok := astRuleGenRecent[fp]; ok && time.Since(t) < astRuleGenCooldown {
		return false
	}
	return true
}

// GenerateASTRulesFromErrors asks the AI for an ast-grep log extractor rule,
// validates it (structure + optional sg dry-run), and persists under rules/ai-*-logs.yml.
// On validation failure the YAML is quarantined outside ruleDirs so ParseAST stays healthy.
func (a *LogAnalyzer) GenerateASTRulesFromErrors(ctx context.Context, errors []SemanticError) (*ASTRuleGenResult, error) {
	if a == nil {
		return nil, fmt.Errorf("nil analyzer")
	}
	if !a.shouldAttemptASTRuleGen(errors) {
		return nil, nil
	}
	a.astRuleGenUsed = true

	lang := inferLanguageFromErrors(errors)
	fp := astRuleFingerprint(lang, errors)
	astRuleGenMu.Lock()
	astRuleGenRecent[fp] = time.Now()
	astRuleGenMu.Unlock()

	samples := collectSampleLines(errors, maxASTRuleSampleLines)
	if len(samples) == 0 {
		return nil, nil
	}

	resp, err := a.askAIForASTRule(ctx, lang, samples, errors)
	if err != nil {
		return nil, err
	}
	if resp == nil || strings.TrimSpace(resp.YAML) == "" {
		return nil, nil
	}

	result, err := PersistValidatedASTRule(ctx, resp.YAML, resp.ID, resp.Language, resp.SampleCode)
	if err != nil {
		return result, err
	}
	if result != nil && result.Validated && result.Path != "" {
		_ = code.EnsureAstGrepRules()
		logger.Info("AST rule gen: wrote validated rule %s → %s (ingest_code may be needed for full LogAlign effect)",
			result.RuleID, result.Path)
	}
	return result, nil
}

func (a *LogAnalyzer) askAIForASTRule(ctx context.Context, lang string, samples []string, errors []SemanticError) (*aiASTRuleResponse, error) {
	fileHints := make([]string, 0, 4)
	for _, e := range errors {
		if e.File != "" && len(fileHints) < 4 {
			fileHints = append(fileHints, e.File)
		}
	}

	prompt := fmt.Sprintf(`You generate an ast-grep YAML rule that extracts log FORMAT strings from source code.
The rule will be used like human-written rules in rules/*-logs.yml: matches must expose metavar $FORMAT (the format/message string literal).

Constraints:
- language: %s (or refine if samples clearly indicate otherwise; one of: go, javascript, typescript, python, java, csharp, cpp)
- id MUST start with "ai-generated-" and MUST end with "-logs" (example: ai-generated-go-zap-logs)
- Use rule.any with pattern entries; every pattern that captures a message must bind $FORMAT
- Prefer logger call shapes NOT already covered by generic Info/Error/Printf patterns (e.g. Infow, Errorw, With().Info, slog.Log, custom wrappers)
- Do NOT invent unrelated languages. Keep YAML small (< 8KB).
- Also provide a short sample_code snippet in that language that MUST match at least one pattern and include a realistic format string literal.

Return ONLY JSON (no markdown):
{"language":"...","id":"ai-generated-...-logs","yaml":"<full yaml document>","sample_code":"<snippet>","rationale":"<one sentence>"}

Project: %s
File hints: %s
Sample runtime log lines:
%s
`, lang, a.Project, strings.Join(fileHints, ", "), strings.Join(samples, "\n"))

	contents := []ai.Content{{Parts: []ai.Part{{Text: prompt}}}}
	cfg := ai.GenerationConfig{Temperature: 0.1}
	candidate, err := a.AI.GenerateContent(ctx, contents, cfg)
	if err != nil {
		return nil, err
	}
	if candidate.UsageMetadata != nil && a.CostTracker != nil {
		a.CostTracker.RecordUsage(ctx, a.LogFileID,
			candidate.UsageMetadata.PromptTokenCount, candidate.UsageMetadata.CandidatesTokenCount)
	}
	if len(candidate.Content.Parts) == 0 {
		return nil, fmt.Errorf("empty AI response")
	}
	return parseAIASTRuleJSON(candidate.Content.Parts[0].Text)
}

func parseAIASTRuleJSON(raw string) (*aiASTRuleResponse, error) {
	clean := strings.TrimSpace(raw)
	if strings.HasPrefix(clean, "```") {
		if idx := strings.Index(clean, "\n"); idx != -1 {
			clean = clean[idx+1:]
		}
	}
	clean = strings.TrimSpace(clean)
	if strings.HasSuffix(strings.TrimRight(clean, " \t\r\n"), "```") {
		clean = strings.TrimRight(clean, " \t\r\n")
		clean = clean[:len(clean)-3]
	}
	clean = strings.TrimSpace(clean)
	var resp aiASTRuleResponse
	if err := json.Unmarshal([]byte(clean), &resp); err != nil {
		return nil, fmt.Errorf("parse AI AST rule JSON: %w", err)
	}
	resp.YAML = strings.TrimSpace(resp.YAML)
	resp.ID = strings.TrimSpace(resp.ID)
	resp.Language = normalizeASTLanguage(resp.Language)
	resp.SampleCode = strings.TrimSpace(resp.SampleCode)
	return &resp, nil
}

// PersistValidatedASTRule validates YAML then writes to rules/ or quarantine.
// Delegates to the unified code ingest synthesizer (logs kind).
func PersistValidatedASTRule(ctx context.Context, yamlText, preferredID, language, sampleCode string) (*ASTRuleGenResult, error) {
	res, err := code.PersistValidatedRule(ctx, yamlText, preferredID, language, sampleCode, code.RuleKindLogs)
	if res == nil {
		return nil, err
	}
	return &ASTRuleGenResult{
		Path: res.Path, Quarantine: res.Quarantine, RuleID: res.RuleID,
		Language: res.Language, Validated: res.Validated,
	}, err
}

var (
	reYAMLID        = regexp.MustCompile(`(?m)^id:\s*['"]?([A-Za-z0-9_-]+)['"]?\s*$`)
	reYAMLLang      = regexp.MustCompile(`(?m)^language:\s*['"]?([A-Za-z0-9_+-]+)['"]?\s*$`)
	reHasFORMAT     = regexp.MustCompile(`\$FORMAT\b`)
	reHasPattern    = regexp.MustCompile(`(?m)^\s*-?\s*pattern:`)
	reDangerousYAML = regexp.MustCompile(`(?i)(!!|<<\s*\*|file://|/etc/passwd)`)
	reDigits        = regexp.MustCompile(`\d+`)
)

func structuralValidateASTRule(yamlText, preferredID, language string) (string, error) {
	if reDangerousYAML.MatchString(yamlText) {
		return "", fmt.Errorf("rejected dangerous yaml tokens")
	}
	id := preferredID
	if m := reYAMLID.FindStringSubmatch(yamlText); len(m) == 2 {
		id = m[1]
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("missing rule id")
	}
	if !strings.HasPrefix(id, "ai-") {
		id = "ai-" + id
	}
	if !strings.HasSuffix(id, "-logs") {
		return "", fmt.Errorf("rule id %q must end with -logs", id)
	}
	langInYAML := language
	if m := reYAMLLang.FindStringSubmatch(yamlText); len(m) == 2 {
		langInYAML = normalizeASTLanguage(m[1])
	}
	if langInYAML == "" {
		return "", fmt.Errorf("missing language")
	}
	if !reHasFORMAT.MatchString(yamlText) {
		return "", fmt.Errorf("rule must bind $FORMAT metavar")
	}
	if !reHasPattern.MatchString(yamlText) {
		return "", fmt.Errorf("rule must contain pattern entries")
	}
	// Ensure id/language in document match sanitized values (rewrite if needed is caller's job;
	// we only require presence here).
	_ = langInYAML
	return id, nil
}

func validateASTRuleWithSG(ctx context.Context, yamlText, ruleID, language, sampleCode string) error {
	sgBin := code.SGBinaryPath()
	if sgBin == "" {
		return fmt.Errorf("sg binary not found")
	}
	if _, err := os.Stat(sgBin); err != nil {
		// Best-effort: try ensuring rules/binary layout without forcing download.
		_ = code.EnsureAstGrepRules()
		sgBin = code.SGBinaryPath()
		if _, err2 := os.Stat(sgBin); err2 != nil {
			if os.Getenv("IBIS_AST_RULE_STRUCTURAL_ONLY") == "1" {
				return nil
			}
			return fmt.Errorf("sg binary missing: %w", err2)
		}
	}

	tmpRoot, err := os.MkdirTemp("", "ibis-sg-rule-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpRoot)

	rulesDir := filepath.Join(tmpRoot, "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		return err
	}
	cfgPath := filepath.Join(tmpRoot, "sgconfig.yml")
	if err := os.WriteFile(cfgPath, []byte("ruleDirs:\n  - rules\n"), 0644); err != nil {
		return err
	}
	// Normalize id in YAML document to ruleID.
	doc := ensureYAMLID(yamlText, ruleID, language)
	rulePath := filepath.Join(rulesDir, ruleID+".yml")
	if err := os.WriteFile(rulePath, []byte(doc), 0644); err != nil {
		return err
	}

	ext := languageExt(language)
	sample := sampleCode
	if strings.TrimSpace(sample) == "" {
		sample = minimalSmokeSnippet(language)
	}
	samplePath := filepath.Join(tmpRoot, "sample"+ext)
	if err := os.WriteFile(samplePath, []byte(sample), 0644); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, sgBin, "scan", "--json=stream", "--config", cfgPath, samplePath)
	cmd.Dir = tmpRoot
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	if err != nil {
		// sg may exit non-zero on zero matches depending on version; inspect output.
		if looksLikeSGRuleError(outStr) {
			preview := outStr
			if len(preview) > 400 {
				preview = preview[:400]
			}
			return fmt.Errorf("sg rejected rule: %s", preview)
		}
	}
	if looksLikeSGRuleError(outStr) {
		preview := outStr
		if len(preview) > 400 {
			preview = preview[:400]
		}
		return fmt.Errorf("sg rule error: %s", preview)
	}
	// If sample_code was provided, require at least one JSON match line.
	if strings.TrimSpace(sampleCode) != "" {
		matched := false
		for _, line := range strings.Split(outStr, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !strings.HasPrefix(line, "{") {
				continue
			}
			if strings.Contains(line, ruleID) || strings.Contains(line, `"ruleId"`) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("sg produced no matches for provided sample_code")
		}
	}
	return nil
}

func looksLikeSGRuleError(out string) bool {
	lower := strings.ToLower(out)
	// Prefer explicit failure markers; avoid matching JSON rule payloads.
	hard := []string{
		"failed to parse",
		"invalid rule",
		"cannot parse rule",
		"error while loading rule",
		"serde_yaml",
		"syntax error in rule",
	}
	for _, n := range hard {
		if strings.Contains(lower, n) {
			return true
		}
	}
	// stderr-only error without any JSON match objects
	if strings.Contains(lower, "error:") && !strings.Contains(out, `"ruleId"`) && !strings.Contains(out, `"ruleid"`) {
		return true
	}
	return false
}

func ensureYAMLID(yamlText, ruleID, language string) string {
	doc := yamlText
	if reYAMLID.MatchString(doc) {
		doc = reYAMLID.ReplaceAllString(doc, "id: "+ruleID)
	} else {
		doc = "id: " + ruleID + "\n" + doc
	}
	if language != "" {
		if reYAMLLang.MatchString(doc) {
			doc = reYAMLLang.ReplaceAllString(doc, "language: "+language)
		} else {
			doc = "language: " + language + "\n" + doc
		}
	}
	return doc
}

func writeASTRuleFile(yamlText, ruleID string) (string, error) {
	root := code.SGConfigDir()
	if root == "" {
		return "", fmt.Errorf("sgconfig root not found")
	}
	rulesDir := filepath.Join(root, "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		return "", err
	}
	doc := ensureYAMLID(yamlText, ruleID, "")
	path := filepath.Join(rulesDir, ruleID+".yml")
	if err := os.WriteFile(path, []byte(doc+"\n"), 0644); err != nil {
		return "", err
	}
	// Also mirror into testrun/rules when present (harness often uses that tree).
	testrunRules := filepath.Join(root, "testrun", "rules")
	if st, err := os.Stat(testrunRules); err == nil && st.IsDir() {
		_ = os.WriteFile(filepath.Join(testrunRules, ruleID+".yml"), []byte(doc+"\n"), 0644)
	}
	return path, nil
}

func quarantineASTRule(yamlText, ruleID, reason string) (string, error) {
	root := code.SGConfigDir()
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = cwd
	}
	dir := filepath.Join(root, astRuleQuarantineDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if ruleID == "" {
		ruleID = "ai-unknown-logs"
	}
	name := ruleID + "_" + time.Now().Format("20060102T150405") + ".yml"
	path := filepath.Join(dir, name)
	body := fmt.Sprintf("# QUARANTINED: %s\n# reason: %s\n%s\n", time.Now().UTC().Format(time.RFC3339), sanitizeReason(reason), yamlText)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		return "", err
	}
	logger.Warn("AST rule gen: quarantined %s (%s)", path, sanitizeReason(reason))
	return path, nil
}

func sanitizeReason(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func inferLanguageFromErrors(errors []SemanticError) string {
	counts := map[string]int{}
	for _, e := range errors {
		lang := languageFromPath(e.File)
		if lang != "" {
			counts[lang]++
		}
	}
	best, bestN := "", 0
	for lang, n := range counts {
		if n > bestN {
			best, bestN = lang, n
		}
	}
	if best != "" {
		return best
	}
	// No in-repo source hint → skip Path D (regex Path C remains the fallback).
	return ""
}

func languageFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".py":
		return "python"
	case ".java":
		return "java"
	case ".cs":
		return "csharp"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	default:
		return ""
	}
}

func normalizeASTLanguage(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	switch lang {
	case "js":
		return "javascript"
	case "ts":
		return "typescript"
	case "c#", "c-sharp":
		return "csharp"
	case "c++":
		return "cpp"
	default:
		return lang
	}
}

func languageExt(lang string) string {
	switch normalizeASTLanguage(lang) {
	case "go":
		return ".go"
	case "javascript":
		return ".js"
	case "typescript":
		return ".ts"
	case "python":
		return ".py"
	case "java":
		return ".java"
	case "csharp":
		return ".cs"
	case "cpp":
		return ".cpp"
	default:
		return ".txt"
	}
}

func minimalSmokeSnippet(lang string) string {
	switch normalizeASTLanguage(lang) {
	case "go":
		return "package p\nfunc f(l interface{ Infow(string, ...interface{}) }) {\n\tl.Infow(\"smoke format %s\", \"x\")\n}\n"
	case "javascript", "typescript":
		return "logger.infow(\"smoke format %s\", \"x\");\n"
	case "python":
		return "logger.info(\"smoke format %s\", \"x\")\n"
	default:
		return "// smoke\n"
	}
}

func collectSampleLines(errors []SemanticError, max int) []string {
	out := make([]string, 0, max)
	seen := map[string]bool{}
	for _, e := range errors {
		for _, cand := range []string{e.StackTrace, e.Template} {
			cand = strings.TrimSpace(cand)
			if cand == "" || seen[cand] {
				continue
			}
			seen[cand] = true
			// Keep first line only for templates that span blocks.
			if i := strings.IndexByte(cand, '\n'); i >= 0 {
				cand = cand[:i]
			}
			out = append(out, cand)
			if len(out) >= max {
				return out
			}
		}
	}
	return out
}

func astRuleFingerprint(lang string, errors []SemanticError) string {
	h := sha256.New()
	h.Write([]byte(lang))
	for _, line := range collectSampleLines(errors, 6) {
		cleaned := reDigits.ReplaceAllString(line, "#")
		h.Write([]byte(cleaned))
	}
	return hex.EncodeToString(h.Sum(nil))
}
