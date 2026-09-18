package code

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/terenzif/ibis-server/internal/logger"
)

type ASTChunk struct {
	SymbolName string
	Kind       string
	Content    string
	StartLine  uint32
	EndLine    uint32
}

type CallEdge struct {
	CallerName string
	CalleeName string
	StartLine  uint32
}

type LogTemplate struct {
	FormatString string
	Regex        string
	SourceFile   string
	SourceLine   uint32
}

type SGMatch struct {
	Text          string  `json:"text"`
	Range         SGRange `json:"range"`
	File          string  `json:"file"`
	RuleId        string  `json:"ruleId"`
	MetaVariables struct {
		Single map[string]struct {
			Text string `json:"text"`
		} `json:"single"`
	} `json:"metaVariables"`
}

type SGRange struct {
	Start struct {
		Line uint32 `json:"line"`
	} `json:"start"`
	End struct {
		Line uint32 `json:"line"`
	} `json:"end"`
}

// parseFormatToRegex converts a printf-style format into a RE2 substring pattern.
// Anchors are intentionally omitted so runtime prefixes (timestamps, [INFO], slog
// key=value tails) still match the static message body extracted from source.
func parseFormatToRegex(format string) string {
	format = strings.TrimSpace(format)
	format = strings.Trim(format, "\"`'")
	if format == "" {
		return ""
	}

	replacer := strings.NewReplacer(
		`\`, `\\`,
		".", `\.`,
		"+", `\+`,
		"*", `\*`,
		"?", `\?`,
		"(", `\(`,
		")", `\)`,
		"[", `\[`,
		"]", `\]`,
		"{", `\{`,
		"}", `\}`,
		"^", `\^`,
		"$", `\$`,
		"|", `\|`,
	)
	regexStr := replacer.Replace(format)

	// Longer verbs first so "%+v" is not partially eaten by "%v".
	regexStr = strings.ReplaceAll(regexStr, "%+v", "(.*?)")
	regexStr = strings.ReplaceAll(regexStr, "%#v", "(.*?)")
	regexStr = strings.ReplaceAll(regexStr, "%s", "(.*?)")
	regexStr = strings.ReplaceAll(regexStr, "%d", `([-+]?\d+)`)
	regexStr = strings.ReplaceAll(regexStr, "%v", "(.*?)")
	regexStr = strings.ReplaceAll(regexStr, "%w", "(.*?)")
	regexStr = strings.ReplaceAll(regexStr, "%f", `([-+]?\d+(?:\.\d+)?)`)
	regexStr = strings.ReplaceAll(regexStr, "%t", `(true|false)`)
	regexStr = strings.ReplaceAll(regexStr, "%x", `([0-9a-fA-F]+)`)
	regexStr = strings.ReplaceAll(regexStr, "%q", `(.*?)`)

	return regexStr
}

// isUsefulLogTemplate rejects formats/regexes that are too short or wildcard-only
// (e.g. a lone "%w" → "(.*?)" would LogAlign-match every line).
func isUsefulLogTemplate(format, regex string) bool {
	f := strings.TrimSpace(format)
	f = strings.Trim(f, "\"`'")
	if len(f) < 12 {
		return false
	}
	r := strings.TrimSpace(regex)
	if r == "" || r == "(.*?)" || r == "(.*)" || r == ".+" || r == ".*" {
		return false
	}
	// Strip common wildcard tokens; require remaining literal mass.
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
	return literalRunes >= 8
}

// SGConfigDir locates a directory containing sgconfig.yml (+ rules/).
// Order: IBIS_SG_ROOT env, executable dir, cwd, then walk parents of cwd.
func SGConfigDir() string { return findSGConfigDir() }

// SGBinaryPath resolves the ast-grep (sg) executable for the current config root.
func SGBinaryPath() string { return resolveSGBinary(findSGConfigDir()) }

// findSGConfigDir locates a directory containing sgconfig.yml (+ rules/).
// Order: IBIS_SG_ROOT env, executable dir, cwd, then walk parents of cwd.
func findSGConfigDir() string {
	if env := strings.TrimSpace(os.Getenv("IBIS_SG_ROOT")); env != "" {
		if hasSGConfig(env) {
			return env
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if hasSGConfig(dir) {
			return dir
		}
		// Common layout: testrun/ibis-assistant.exe with rules at repo root.
		parent := filepath.Dir(dir)
		if hasSGConfig(parent) {
			return parent
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		dir := cwd
		for i := 0; i < 8; i++ {
			if hasSGConfig(dir) {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return ""
}

func hasSGConfig(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "sgconfig.yml"))
	return err == nil
}

func resolveSGBinary(configDir string) string {
	binName := "sg"
	if runtime.GOOS == "windows" {
		binName = "sg.exe"
	}
	// Prefer next to config (testrun), then next to executable, then PATH.
	candidates := []string{}
	if configDir != "" {
		candidates = append(candidates,
			filepath.Join(configDir, binName),
			filepath.Join(configDir, "testrun", binName),
		)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), binName))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, binName),
			filepath.Join(cwd, "testrun", binName),
		)
		// Walk a few parents for testrun/sg.exe (go test cwd is often a package dir).
		dir := cwd
		for i := 0; i < 6; i++ {
			candidates = append(candidates, filepath.Join(dir, "testrun", binName), filepath.Join(dir, binName))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	candidates = append(candidates, binName, "sg.exe", "sg")
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return binName
}

func ParseAST(ctx context.Context, filePath string, content []byte) ([]ASTChunk, []LogTemplate, []CallEdge, error) {
	ext := filepath.Ext(filePath)
	if ext == "" {
		ext = ".txt"
	}
	// Ensure template-like extensions are discoverable as HTML hosts (languageGlobs).
	if IsHTMLHostExt(filePath) {
		_ = EnsureLanguageGlob(ext, "html")
	}
	return scanSGContent(ctx, filePath, content, ext, 0)
}

// scanSGContent runs ast-grep on content written to a temp file with the given extension.
// lineBias is added to reported start/end lines (0 for host file; hostLine-1 for extracted regions).
func scanSGContent(ctx context.Context, sourcePath string, content []byte, ext string, lineBias int) ([]ASTChunk, []LogTemplate, []CallEdge, error) {
	tmpFile, err := os.CreateTemp("", "sg_ast_*"+ext)
	if err != nil {
		return nil, nil, nil, err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(content); err != nil {
		return nil, nil, nil, err
	}
	tmpFile.Close()

	configDir := findSGConfigDir()
	sgPath := resolveSGBinary(configDir)

	args := []string{"scan", "--json=stream"}
	if configDir != "" {
		cfg := filepath.Join(configDir, "sgconfig.yml")
		args = append(args, "--config", cfg)
	}
	args = append(args, tmpFile.Name())

	cmd := exec.CommandContext(ctx, sgPath, args...)
	if configDir != "" {
		cmd.Dir = configDir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil && stdout.Len() == 0 {
		errPreview := strings.TrimSpace(stderr.String())
		if len(errPreview) > 240 {
			errPreview = errPreview[:240]
		}
		logger.Debug("ast-grep scan failed for %s (config=%s): %v %s", sourcePath, configDir, runErr, errPreview)
	}

	var chunks []ASTChunk
	var logs []LogTemplate
	var calls []CallEdge

	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var match SGMatch
		if err := json.Unmarshal(line, &match); err != nil {
			continue
		}

		start := match.Range.Start.Line + 1 + uint32(lineBias)
		end := match.Range.End.Line + 1 + uint32(lineBias)

		if strings.HasSuffix(match.RuleId, "-chunk") {
			chunks = append(chunks, ASTChunk{
				SymbolName: guessSymbolName(match.Text, match.RuleId),
				Kind:       match.RuleId,
				Content:    match.Text,
				StartLine:  start,
				EndLine:    end,
			})
		} else if strings.HasSuffix(match.RuleId, "-logs") {
			formatStr := ""
			if fmtVar, ok := match.MetaVariables.Single["FORMAT"]; ok {
				formatStr = fmtVar.Text
			}
			if strings.Trim(formatStr, "\"`'") == "" {
				continue
			}
			rx := parseFormatToRegex(formatStr)
			if !isUsefulLogTemplate(formatStr, rx) {
				continue
			}
			logs = append(logs, LogTemplate{
				FormatString: formatStr,
				Regex:        rx,
				SourceFile:   sourcePath,
				SourceLine:   start,
			})
		} else if strings.HasSuffix(match.RuleId, "-calls") {
			callee := strings.SplitN(match.Text, "(", 2)[0]
			calls = append(calls, CallEdge{
				CallerName: "Unknown",
				CalleeName: callee,
				StartLine:  start,
			})
		}
	}

	return chunks, logs, calls, nil
}

func guessSymbolName(text, ruleID string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "Snippet"
	}
	// method / function first line
	first := text
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		first = text[:i]
	}
	first = strings.TrimSpace(first)
	for _, prefix := range []string{"public ", "private ", "protected ", "internal ", "static ", "async ", "void ", "function ", "func "} {
		first = strings.TrimPrefix(first, prefix)
	}
	if i := strings.IndexByte(first, '('); i > 0 {
		name := strings.TrimSpace(first[:i])
		parts := strings.Fields(name)
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	if strings.HasPrefix(first, "class ") {
		rest := strings.TrimSpace(strings.TrimPrefix(first, "class "))
		if i := strings.IndexAny(rest, " {"); i > 0 {
			return rest[:i]
		}
		return rest
	}
	_ = ruleID
	return "Snippet"
}

