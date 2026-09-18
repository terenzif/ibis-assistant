package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/terenzif/ibis-assistant/internal/logger"
	"github.com/terenzif/ibis-assistant/internal/schema"
)

// InitSchema initializes the database schema by defining tables and indexes.
func InitSchema(ctx context.Context, dbClient Executor) error {
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

		_, err := dbClient.Execute(ctx, fullStmt)
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
	if err := ClearAbsoluteFileRecords(ctx, dbClient); err != nil {
		logger.Warn("[WARN] Relative path migration warning: %v", err)
	}

	return nil
}

// EnsureIndexes is used to recreate only indexes (e.g. after schema change)
func EnsureIndexes(ctx context.Context, dbClient Executor) error {
	logger.Info("[INFO] Ensuring Database Indexes...")
	sql := schema.GenerateInitSQL()
	if _, err := dbClient.Execute(ctx, sql); err != nil {
		return fmt.Errorf("failed to ensure indexes: %w", err)
	}
	return nil
}
