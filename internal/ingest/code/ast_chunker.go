package code

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ASTChunk struct {
	SymbolName string
	Kind       string
	Content    string
	StartLine  uint32
	EndLine    uint32
}

type LogTemplate struct {
	FormatString string
	Regex        string
	SourceFile   string
	SourceLine   uint32
}

type SGMatch struct {
	Text          string `json:"text"`
	Range         SGRange `json:"range"`
	File          string `json:"file"`
	RuleId        string `json:"ruleId"`
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

func parseFormatToRegex(format string) string {
	format = strings.Trim(format, "\"`'")
	
	replacer := strings.NewReplacer(
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
		"\\", `\\`,
	)
	regexStr := replacer.Replace(format)

	regexStr = strings.ReplaceAll(regexStr, "%s", "(.*?)")
	regexStr = strings.ReplaceAll(regexStr, "%d", `([-+]?\d+)`)
	regexStr = strings.ReplaceAll(regexStr, "%v", "(.*?)")
	regexStr = strings.ReplaceAll(regexStr, "%+v", "(.*?)")
	regexStr = strings.ReplaceAll(regexStr, "%w", "(.*?)")

	return "^" + regexStr + "$"
}

func ParseAST(ctx context.Context, filePath string, content []byte) ([]ASTChunk, []LogTemplate, error) {
	// Write to a temporary file since we get content from git blob directly
	tmpFile, err := os.CreateTemp("", "sg_ast_*.tmp" + filepath.Ext(filePath))
	if err != nil {
		return nil, nil, err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(content); err != nil {
		return nil, nil, err
	}
	tmpFile.Close()

	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	sgPath := filepath.Join(exeDir, "sg.exe")
	if _, err := os.Stat(sgPath); os.IsNotExist(err) {
		sgPath = "sg.exe"
	}

	cmd := exec.CommandContext(ctx, sgPath, "scan", "--json=stream", tmpFile.Name())
	cmd.Dir = exeDir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run()

	var chunks []ASTChunk
	var logs []LogTemplate

	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		var match SGMatch
		if err := json.Unmarshal(line, &match); err != nil {
			continue
		}
		
		if strings.HasSuffix(match.RuleId, "-chunk") {
			chunks = append(chunks, ASTChunk{
				SymbolName: "Snippet", 
				Kind:       match.RuleId,
				Content:    match.Text,
				StartLine:  match.Range.Start.Line + 1,
				EndLine:    match.Range.End.Line + 1,
			})
		} else if strings.HasSuffix(match.RuleId, "-logs") {
			formatStr := ""
			if fmtVar, ok := match.MetaVariables.Single["FORMAT"]; ok {
				formatStr = fmtVar.Text
			}
			logs = append(logs, LogTemplate{
				FormatString: formatStr,
				Regex:        parseFormatToRegex(formatStr),
				SourceFile:   filePath,
				SourceLine:   match.Range.Start.Line + 1,
			})
		}
	}

	return chunks, logs, nil
}
