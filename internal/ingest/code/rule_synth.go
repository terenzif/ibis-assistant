package code

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

	"github.com/terenzif/ibis-assistant/internal/ai"
	"github.com/terenzif/ibis-assistant/internal/logger"
)

// RuleKind selects which ParseAST suffix family a synthesized rule belongs to.
type RuleKind string

const (
	RuleKindLogs  RuleKind = "logs"
	RuleKindChunk RuleKind = "chunk"
	RuleKindCalls RuleKind = "calls"
)

const (
	maxSynthYAMLBytes   = 24 << 10
	ruleQuarantineDir   = "rules_quarantine"
	maxSynthPerExtBatch = 2
)

var (
	synthMu       sync.Mutex
	synthRecent   = map[string]time.Time{}
	synthCooldown = 30 * time.Minute

	reYAMLID        = regexp.MustCompile(`(?m)^id:\s*['"]?([A-Za-z0-9_-]+)['"]?\s*$`)
	reYAMLLang      = regexp.MustCompile(`(?m)^language:\s*['"]?([A-Za-z0-9_+-]+)['"]?\s*$`)
	reHasFORMAT     = regexp.MustCompile(`\$FORMAT\b`)
	reHasPattern    = regexp.MustCompile(`(?m)^\s*-?\s*pattern:`)
	reHasKind       = regexp.MustCompile(`(?m)^\s*-?\s*kind:`)
	reDangerousYAML = regexp.MustCompile(`(?i)(!!|<<\s*\*|file://|/etc/passwd)`)
	reSafeRuleID    = regexp.MustCompile(`[^A-Za-z0-9_-]+`)
)

// RulePersistResult is the outcome of validating + writing one YAML rule.
type RulePersistResult struct {
	Path       string
	Quarantine string
	RuleID     string
	Language   string
	Kind       RuleKind
	Validated  bool
}

// SampleFile is a short excerpt used to ask the AI for ingest rules.
type SampleFile struct {
	RelPath string
	Content string // may be truncated
}

type aiRuleResponse struct {
	Language     string `json:"language"`
	ID           string `json:"id"`
	Kind         string `json:"kind"` // logs|chunk|calls
	YAML         string `json:"yaml"`
	SampleCode   string `json:"sample_code"`
	Rationale    string `json:"rationale"`
	LanguageGlob string `json:"language_glob"` // optional host lang for sg languageGlobs (e.g. html)
}

// PersistValidatedRule validates ast-grep YAML and writes rules/ai-generated-*-{logs|chunk|calls}.yml.
// Shared by log Path-D and code-ingest synthesizer (extension-agnostic).
func PersistValidatedRule(ctx context.Context, yamlText, preferredID, language, sampleCode string, kind RuleKind) (*RulePersistResult, error) {
	yamlText = strings.TrimSpace(yamlText)
	if yamlText == "" {
		return nil, fmt.Errorf("empty yaml")
	}
	if len(yamlText) > maxSynthYAMLBytes {
		return nil, fmt.Errorf("yaml too large (%d)", len(yamlText))
	}
	kind = normalizeRuleKind(kind, preferredID)
	language = normalizeSynthLanguage(language)

	ruleID, err := structuralValidateRule(yamlText, preferredID, language, kind)
	if err != nil {
		qPath, qErr := quarantineRule(yamlText, preferredID, err.Error())
		res := &RulePersistResult{RuleID: preferredID, Language: language, Kind: kind, Quarantine: qPath}
		if qErr != nil {
			return res, fmt.Errorf("structural validation failed: %w (quarantine: %v)", err, qErr)
		}
		return res, fmt.Errorf("structural validation failed: %w", err)
	}

	if err := validateRuleWithSG(ctx, yamlText, ruleID, language, sampleCode, kind); err != nil {
		qPath, qErr := quarantineRule(yamlText, ruleID, err.Error())
		res := &RulePersistResult{RuleID: ruleID, Language: language, Kind: kind, Quarantine: qPath}
		if qErr != nil {
			return res, fmt.Errorf("sg validation failed: %w (quarantine: %v)", err, qErr)
		}
		return res, fmt.Errorf("sg validation failed: %w", err)
	}

	path, err := writeRuleFile(yamlText, ruleID, language)
	if err != nil {
		return &RulePersistResult{RuleID: ruleID, Language: language, Kind: kind}, err
	}
	_ = EnsureAstGrepRules()
	return &RulePersistResult{
		Path: path, RuleID: ruleID, Language: language, Kind: kind, Validated: true,
	}, nil
}

// ContentGenerator is the AI surface used by rule synthesis (implemented by *ai.Client).
type ContentGenerator interface {
	IsFunctional() bool
	GenerateContent(ctx context.Context, contents []ai.Content, config ai.GenerationConfig) (ai.Candidate, error)
}

// SynthesizeIngestRules asks the AI for chunk/calls (and optional logs) rules for unfamiliar files.
// Cost-bounded: at most maxSynthPerExtBatch rules per extension fingerprint cooldown.
func SynthesizeIngestRules(ctx context.Context, aiClient ContentGenerator, samples []SampleFile) ([]RulePersistResult, error) {
	if aiClient == nil || !aiClient.IsFunctional() || len(samples) == 0 {
		return nil, nil
	}
	ext := strings.ToLower(filepath.Ext(samples[0].RelPath))
	if ext == "" {
		ext = ".unknown"
	}
	fp := synthFingerprint(ext, samples)
	synthMu.Lock()
	if t, ok := synthRecent[fp]; ok && time.Since(t) < synthCooldown {
		synthMu.Unlock()
		return nil, nil
	}
	synthRecent[fp] = time.Now()
	synthMu.Unlock()

	// Bootstrap languageGlob before AI when we already know a sensible host.
	if host := SuggestHostLanguage(samples[0].RelPath); host != "" && IsHTMLHostExt(samples[0].RelPath) {
		_ = EnsureLanguageGlob(ext, host)
	}

	prompt := buildIngestSynthPrompt(ext, samples)
	contents := []ai.Content{{Parts: []ai.Part{{Text: prompt}}}}
	candidate, err := aiClient.GenerateContent(ctx, contents, ai.GenerationConfig{Temperature: 0.1})
	if err != nil {
		return nil, err
	}
	if len(candidate.Content.Parts) == 0 {
		return nil, fmt.Errorf("empty AI response")
	}
	rules, err := parseAIRuleListJSON(candidate.Content.Parts[0].Text)
	if err != nil {
		return nil, err
	}

	var out []RulePersistResult
	for i, r := range rules {
		if i >= maxSynthPerExtBatch {
			break
		}
		if g := strings.TrimSpace(r.LanguageGlob); g != "" {
			_ = EnsureLanguageGlob(ext, g)
		} else if host := SuggestHostLanguage(samples[0].RelPath); host != "" && r.Language == host {
			_ = EnsureLanguageGlob(ext, host)
		}
		kind := normalizeRuleKind(RuleKind(r.Kind), r.ID)
		res, err := PersistValidatedRule(ctx, r.YAML, r.ID, r.Language, r.SampleCode, kind)
		if res != nil {
			out = append(out, *res)
		}
		if err != nil {
			continue
		}
		if res != nil && res.Validated {
			logger.Info("Rule synth: wrote %s (%s) → %s", res.RuleID, res.Kind, res.Path)
		}
	}
	return out, nil
}

// MaybeSynthesizeForSparseAST runs after ParseAST when extraction is empty/weak for any extension.
// Returns true if any rule was persisted (caller may re-scan).
func MaybeSynthesizeForSparseAST(ctx context.Context, aiClient ContentGenerator, relPath string, content []byte, chunks []ASTChunk, calls []CallEdge) bool {
	if aiClient == nil || !aiClient.IsFunctional() {
		return false
	}
	if !NeedsRuleSynthesis(relPath, chunks, calls, content) {
		return false
	}
	excerpt := string(content)
	if len(excerpt) > 4000 {
		excerpt = excerpt[:4000]
	}
	results, err := SynthesizeIngestRules(ctx, aiClient, []SampleFile{{RelPath: relPath, Content: excerpt}})
	if err != nil {
		return false
	}
	for _, r := range results {
		if r.Validated && r.Path != "" {
			return true
		}
	}
	return false
}

func buildIngestSynthPrompt(ext string, samples []SampleFile) string {
	hostHint := ""
	if len(samples) > 0 {
		if h := SuggestHostLanguage(samples[0].RelPath); h != "" {
			hostHint = h
		}
	}
	var b strings.Builder
	b.WriteString(`You generate ast-grep YAML rules for Ibis code ingest of unfamiliar, polyglot, or legacy source files.
This must work for ANY extension (templates, DSLs, mixed stacks)—not a single framework.

Rules are consumed by ParseAST: id MUST start with "ai-generated-" and end with "-chunk", "-calls", or "-logs".
- *-chunk: structural symbols (kind: function_declaration / method_declaration / class_declaration / script_element / element / …)
- *-calls: call sites (kind: call_expression or equivalent patterns)
- *-logs: only if logger format strings appear; must bind $FORMAT

Pick a built-in ast-grep language: html, javascript, typescript, csharp, css, go, python, java, cpp, ruby, php, …
If the file extension is not natively mapped, set "language_glob" to the host language (often "html" for markup templates so embedded <script>/<style> inject as JS/CSS).

Keep each YAML < 6KB. Provide sample_code that MUST match at least one pattern/kind.

Return ONLY a JSON array:
[{"language":"...","language_glob":"html|","id":"ai-generated-...-chunk","kind":"chunk","yaml":"...","sample_code":"...","rationale":"..."}]

Extension: `)
	b.WriteString(ext)
	if hostHint != "" {
		b.WriteString("\nSuggested host language: ")
		b.WriteString(hostHint)
	}
	b.WriteString("\nSamples:\n")
	for _, s := range samples {
		b.WriteString("--- ")
		b.WriteString(s.RelPath)
		b.WriteString(" ---\n")
		b.WriteString(s.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func parseAIRuleListJSON(raw string) ([]aiRuleResponse, error) {
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
	var list []aiRuleResponse
	if err := json.Unmarshal([]byte(clean), &list); err != nil {
		// Single object fallback
		var one aiRuleResponse
		if err2 := json.Unmarshal([]byte(clean), &one); err2 != nil {
			return nil, fmt.Errorf("parse AI rule JSON: %w", err)
		}
		list = []aiRuleResponse{one}
	}
	for i := range list {
		list[i].YAML = strings.TrimSpace(list[i].YAML)
		list[i].ID = strings.TrimSpace(list[i].ID)
		list[i].Language = normalizeSynthLanguage(list[i].Language)
		list[i].SampleCode = strings.TrimSpace(list[i].SampleCode)
	}
	return list, nil
}

func canonicalizeGeneratedRuleID(id string, kind RuleKind) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "ai-generated-")
	id = strings.TrimPrefix(id, "ai-")
	for _, s := range []string{"-logs", "-chunk", "-calls"} {
		if strings.HasSuffix(id, s) {
			id = strings.TrimSuffix(id, s)
			break
		}
	}
	id = strings.Trim(id, "-_")
	if id == "" {
		id = "rule"
	}
	// Keep filename/id safe
	id = reSafeRuleID.ReplaceAllString(id, "-")
	return "ai-generated-" + id + "-" + string(kind)
}

func normalizeRuleKind(kind RuleKind, id string) RuleKind {
	k := RuleKind(strings.ToLower(strings.TrimSpace(string(kind))))
	switch k {
	case RuleKindLogs, RuleKindChunk, RuleKindCalls:
		return k
	}
	id = strings.ToLower(id)
	switch {
	case strings.HasSuffix(id, "-logs"):
		return RuleKindLogs
	case strings.HasSuffix(id, "-calls"):
		return RuleKindCalls
	case strings.HasSuffix(id, "-chunk"):
		return RuleKindChunk
	default:
		return RuleKindChunk
	}
}

func structuralValidateRule(yamlText, preferredID, language string, kind RuleKind) (string, error) {
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
	id = canonicalizeGeneratedRuleID(id, kind)
	langInYAML := language
	if m := reYAMLLang.FindStringSubmatch(yamlText); len(m) == 2 {
		langInYAML = normalizeSynthLanguage(m[1])
	}
	if langInYAML == "" {
		return "", fmt.Errorf("missing language")
	}
	switch kind {
	case RuleKindLogs:
		if !reHasFORMAT.MatchString(yamlText) {
			return "", fmt.Errorf("logs rule must bind $FORMAT")
		}
		if !reHasPattern.MatchString(yamlText) {
			return "", fmt.Errorf("logs rule must contain pattern entries")
		}
	default:
		if !reHasPattern.MatchString(yamlText) && !reHasKind.MatchString(yamlText) {
			return "", fmt.Errorf("%s rule must contain pattern or kind entries", kind)
		}
	}
	return id, nil
}

func validateRuleWithSG(ctx context.Context, yamlText, ruleID, language, sampleCode string, kind RuleKind) error {
	sgBin := SGBinaryPath()
	if _, err := os.Stat(sgBin); err != nil {
		_ = EnsureAstGrepRules()
		sgBin = SGBinaryPath()
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
	cfg := "ruleDirs:\n  - rules\nlanguageGlobs:\n  html:\n    - \"*.aspx\"\n    - \"*.vue\"\n    - \"*.jsp\"\n    - \"*.erb\"\n    - \"*.svelte\"\n"
	cfgPath := filepath.Join(tmpRoot, "sgconfig.yml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		return err
	}
	doc := ensureSynthYAMLID(yamlText, ruleID, language)
	if err := os.WriteFile(filepath.Join(rulesDir, ruleID+".yml"), []byte(doc), 0644); err != nil {
		return err
	}

	ext := synthLanguageExt(language)
	sample := sampleCode
	if strings.TrimSpace(sample) == "" {
		sample = minimalSynthSnippet(language, kind)
	}
	samplePath := filepath.Join(tmpRoot, "sample"+ext)
	if err := os.WriteFile(samplePath, []byte(sample), 0644); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, sgBin, "scan", "--json=stream", "--config", cfgPath, samplePath)
	cmd.Dir = tmpRoot
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	if looksLikeSGError(outStr) {
		preview := outStr
		if len(preview) > 400 {
			preview = preview[:400]
		}
		return fmt.Errorf("sg rule error: %s", preview)
	}
	_ = err
	if strings.TrimSpace(sampleCode) != "" {
		matched := false
		for _, line := range strings.Split(outStr, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "{") && (strings.Contains(line, ruleID) || strings.Contains(line, `"ruleId"`)) {
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

func looksLikeSGError(out string) bool {
	lower := strings.ToLower(out)
	for _, n := range []string{"failed to parse", "invalid rule", "cannot parse rule", "error while loading rule", "serde_yaml"} {
		if strings.Contains(lower, n) {
			return true
		}
	}
	if strings.Contains(lower, "error:") && !strings.Contains(out, `"ruleId"`) {
		return true
	}
	return false
}

func ensureSynthYAMLID(yamlText, ruleID, language string) string {
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

func writeRuleFile(yamlText, ruleID, language string) (string, error) {
	root := SGConfigDir()
	if root == "" {
		return "", fmt.Errorf("sgconfig root not found")
	}
	rulesDir := filepath.Join(root, "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		return "", err
	}
	doc := ensureSynthYAMLID(yamlText, ruleID, language)
	path := filepath.Join(rulesDir, ruleID+".yml")
	if err := os.WriteFile(path, []byte(doc+"\n"), 0644); err != nil {
		return "", err
	}
	testrunRules := filepath.Join(root, "testrun", "rules")
	if st, err := os.Stat(testrunRules); err == nil && st.IsDir() {
		_ = os.WriteFile(filepath.Join(testrunRules, ruleID+".yml"), []byte(doc+"\n"), 0644)
	}
	return path, nil
}

func quarantineRule(yamlText, ruleID, reason string) (string, error) {
	root := SGConfigDir()
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = cwd
	}
	dir := filepath.Join(root, ruleQuarantineDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if ruleID == "" {
		ruleID = "ai-unknown"
	}
	name := ruleID + "_" + time.Now().Format("20060102T150405") + ".yml"
	path := filepath.Join(dir, name)
	body := fmt.Sprintf("# QUARANTINED: %s\n# reason: %s\n%s\n", time.Now().UTC().Format(time.RFC3339), strings.ReplaceAll(reason, "\n", " "), yamlText)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		return "", err
	}
	logger.Warn("Rule synth: quarantined %s", path)
	return path, nil
}

func normalizeSynthLanguage(lang string) string {
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

func synthLanguageExt(lang string) string {
	switch normalizeSynthLanguage(lang) {
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
	case "html":
		return ".html"
	case "css":
		return ".css"
	default:
		return ".txt"
	}
}

func minimalSynthSnippet(lang string, kind RuleKind) string {
	switch normalizeSynthLanguage(lang) {
	case "html":
		return "<html><body><script>function f(){g()}</script><form runat=\"server\"></form></body></html>\n"
	case "csharp":
		return "class C { void M() { Logger.Error(\"x %s\", y); } }\n"
	case "javascript":
		if kind == RuleKindLogs {
			return "logger.error(\"fail %s\", x);\n"
		}
		return "function f(){ alert(\"hi\"); }\n"
	case "go":
		return "package p\nfunc f(l interface{ Infow(string, ...interface{}) }) { l.Infow(\"x %s\", \"y\") }\n"
	default:
		return "// smoke\n"
	}
}

func synthFingerprint(ext string, samples []SampleFile) string {
	h := sha256.New()
	h.Write([]byte(ext))
	for _, s := range samples {
		h.Write([]byte(s.RelPath))
		n := len(s.Content)
		if n > 512 {
			n = 512
		}
		h.Write([]byte(s.Content[:n]))
	}
	return hex.EncodeToString(h.Sum(nil))
}
