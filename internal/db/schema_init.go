package db

import (
	"strings"

	"github.com/deckonline/knowledge_mcp/internal/logger"
	"github.com/deckonline/knowledge_mcp/internal/schema"
)

// InitSchema initializes the database schema by defining tables and indexes.
func InitSchema(db Executor) error {
	logger.Info("[INFO] Initializing Database Schema...")

	sql := schema.GenerateInitSQL()
	statements := strings.Split(sql, ";")

	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		// Re-append semicolon as Split removes it
		fullStmt := stmt + ";"

		_, err := db.Execute(fullStmt)
		if err != nil {
			// Some statements might fail if already exists, but DEFINE usually handles it or we log it.
			// Specifically for SurrealDB, DEFINE INDEX might fail if it exists but it's usually fine.
			logger.Debug("Schema initialization statement: %s", fullStmt)
			if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
				logger.Warn("Schema init warning on statement [%s]: %v", stmt, err)
			}
		}
	}

	logger.Info("[INFO] Database Schema initialized successfully.")

	// --- [MIGRATION] Handle transition to relative paths ---
	if err := ClearAbsoluteFileRecords(db); err != nil {
		logger.Warn("[WARN] Relative path migration warning: %v", err)
	}

	return nil
}
