package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/config"
	"github.com/deckonline/knowledge_mcp/internal/db"
	"github.com/deckonline/knowledge_mcp/internal/logger"
)

type Reporter struct {
	Cfg *config.Config
	DB  db.Executor
}

func NewReporter(cfg *config.Config, dbClient db.Executor) *Reporter {
	return &Reporter{Cfg: cfg, DB: dbClient}
}

func (r *Reporter) GenerateReport(project string, logFileName string, errorsCount int, cost float64) (string, error) {
	reportName := fmt.Sprintf("report_%s_%s.md", filepath.Base(logFileName), time.Now().Format("20060102150405"))
	reportPath := filepath.Join(r.Cfg.LogsRoot, reportName)

	// In a complete implementation, this would query SurrealDB to group by new vs known errors.
	// We insert basic aggregated metrics here for brevity.
	content := fmt.Sprintf(`# Log Analysis Report
	
**Project:** %s
**Log File:** %s
**Report Date:** %s

## Overview
- **Errors Detected (Current Batch):** %d
- **Estimated AI Token Cost:** ~$%.4f

*Detailed Semantic Maps have been updated in the Knowledge Graph Vector Index. Use the RAG API to query errors.*
`, project, filepath.Base(logFileName), time.Now().Format(time.RFC822), errorsCount, cost)

	err := os.WriteFile(reportPath, []byte(content), 0644)
	if err != nil {
		return "", err
	}
	logger.Info("Generated report at: %s", reportPath)
	return reportPath, nil
}
