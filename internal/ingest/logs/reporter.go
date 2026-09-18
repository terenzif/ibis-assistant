package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/terenzif/ibis-assistant/internal/config"
	"github.com/terenzif/ibis-assistant/internal/db"
	"github.com/terenzif/ibis-assistant/internal/logger"
)

type Reporter struct {
	Cfg *config.Config
	DB  db.Executor
}

func NewReporter(cfg *config.Config, dbClient db.Executor) *Reporter {
	return &Reporter{Cfg: cfg, DB: dbClient}
}

func (r *Reporter) GenerateReport(project string, logFileName string, anomalies []SemanticError, cost float64) (string, error) {
	reportName := fmt.Sprintf("report_%s_%s.md", filepath.Base(logFileName), time.Now().Format("20060102150405"))
	// Write beside the log file when possible; fall back to logs_root.
	dir := filepath.Dir(logFileName)
	if dir == "" || dir == "." {
		dir = r.Cfg.LogsRoot
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	reportPath := filepath.Join(dir, reportName)

	var b strings.Builder
	fmt.Fprintf(&b, `# Log Analysis Report

**Project:** %s
**Log File:** %s
**Report Date:** %s

## Overview
- **Errors Detected (Current Batch):** %d
- **Estimated AI Token Cost:** ~$%.4f

`, project, filepath.Base(logFileName), time.Now().Format(time.RFC822), len(anomalies), cost)

	if len(anomalies) > 0 {
		b.WriteString("## Detected Anomalies\n\n")
		for i, a := range anomalies {
			fmt.Fprintf(&b, "### %d. %s\n\n", i+1, displayOr(a.Category, "Uncategorized"))
			fmt.Fprintf(&b, "- **Severity:** %d\n", a.Severity)
			fileDisp := displayOr(a.File, "(unknown)")
			if a.Line > 0 {
				fileDisp = fmt.Sprintf("%s:%d", fileDisp, a.Line)
			}
			fmt.Fprintf(&b, "- **File:** %s\n", fileDisp)
			if a.Symbol != "" {
				fmt.Fprintf(&b, "- **Symbol:** %s\n", a.Symbol)
			}
			fmt.Fprintf(&b, "- **Cause:** %s\n", displayOr(a.Cause, "(not determined)"))
			if a.Baseline != "" {
				fmt.Fprintf(&b, "- **Baseline:** %s\n", a.Baseline)
			}
			fmt.Fprintf(&b, "- **Template:** `%s`\n\n", escapeInlineCode(displayOr(a.Template, a.Category)))
			if strings.TrimSpace(a.StackTrace) != "" {
				b.WriteString("**Stack / Context:**\n\n```\n")
				b.WriteString(strings.TrimSpace(a.StackTrace))
				b.WriteString("\n```\n\n")
			}
			if len(a.Supersedes) > 0 {
				fmt.Fprintf(&b, "- **Supersedes:** %s\n\n", strings.Join(a.Supersedes, ", "))
			}
		}
	} else {
		b.WriteString("*No semantic anomalies were attached to this report.*\n")
	}

	err := os.WriteFile(reportPath, []byte(b.String()), 0644)
	if err != nil {
		return "", err
	}
	logger.Info("Generated report at: %s", reportPath)
	return reportPath, nil
}

func displayOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func escapeInlineCode(s string) string {
	return strings.ReplaceAll(s, "`", "'")
}

func anomalyCategories(anomalies []SemanticError) []string {
	out := make([]string, 0, len(anomalies))
	for _, a := range anomalies {
		out = append(out, a.Category)
	}
	return out
}
