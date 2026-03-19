package db

import (
	"fmt"
	"strings"

	"github.com/deckonline/knowledge_mcp/internal/logger"
)

// MigrateLegacyFileTable moves data from the legacy 'file' table (reserved keyword in v3)
// to the new 'source_file' table and updates all references.
func MigrateLegacyFileTable(db Executor) error {
	logger.Info("[MIGRATION] Checking for legacy 'file' table records...")

	// 1. Check if 'file' table has records
	// Note: We use backticks because 'file' is a reserved keyword in SurrealDB 3.0.4
	res, err := db.Execute("SELECT count() FROM `file`;")
	if err != nil {
		// If table doesn't exist, it's fine
		if strings.Contains(strings.ToLower(err.Error()), "not found") || 
		   strings.Contains(strings.ToLower(err.Error()), "not exist") {
			return nil
		}
		return fmt.Errorf("failed to check legacy table: %w", err)
	}

	var count int64
	if rows, ok := res.([]interface{}); ok && len(rows) > 0 {
		if row, ok := rows[0].(map[string]interface{}); ok {
			if c, ok := row["count"].(float64); ok { count = int64(c) }
			if c, ok := row["count"].(int64); ok { count = c }
		}
	}

	if count == 0 {
		logger.Debug("[MIGRATION] No records found in legacy 'file' table.")
		return nil
	}

	logger.Info("[MIGRATION] Found %d legacy file records. Starting migration...", count)

	// 2. Perform Migration in a transaction
	// Since SurrealDB doesn't support RENAME TABLE, we copy and update.
	// We use raw SQL for performance and simplicity since ID formats are identical (just prefix change).
	
	migrationSQL := `
		BEGIN TRANSACTION;
		
		-- 1. Copy records from 'file' to 'source_file'
		-- We use string::replace to change the ID prefix from 'file:' to 'source_file:'
		INSERT INTO source_file (SELECT *, id = string::replace(string(id), "file:", "source_file:") FROM ` + "`file`" + `);
		
		-- 2. Update file_chunk references
		UPDATE file_chunk SET file = string::replace(string(file), "file:", "source_file:") WHERE string::starts_with(string(file), "file:");
		
		-- 3. Update 'contains' edge (Repo -> File)
		UPDATE contains SET out = string::replace(string(out), "file:", "source_file:") WHERE string::starts_with(string(out), "file:");
		
		-- 4. Update 'changed' edge (Commit -> File)
		UPDATE changed SET out = string::replace(string(out), "file:", "source_file:") WHERE string::starts_with(string(out), "file:");
		
		-- 5. Update 'related_to' edge (ErrorType -> File)
		UPDATE related_to SET out = string::replace(string(out), "file:", "source_file:") WHERE string::starts_with(string(out), "file:");
		
		-- 6. Clean up (Remove legacy records)
		DELETE ` + "`file`" + `;
		
		COMMIT;
	`

	_, err = db.Execute(migrationSQL)
	if err != nil {
		return fmt.Errorf("migration transaction failed: %w", err)
	}

	logger.Info("[MIGRATION] Successfully migrated %d records from 'file' to 'source_file'.", count)
	return nil
}
