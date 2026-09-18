package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/terenzif/ibis-assistant/internal/logger"
)

// MigrateLegacyFileTable moves data from the legacy 'file' table (reserved keyword in v3)
// to the new 'source_file' table and updates all references.
func MigrateLegacyFileTable(ctx context.Context, db Executor) error {
	logger.Info("[MIGRATION] Checking for legacy 'file' table records...")

	// 1. Check if 'file' table has records
	// Note: We use backticks because 'file' is a reserved keyword in SurrealDB 3.0.4
	res, err := db.Execute(ctx, "SELECT count() FROM `file`;")
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
			if c, ok := row["count"].(float64); ok {
				count = int64(c)
			}
			if c, ok := row["count"].(int64); ok {
				count = c
			}
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
		INSERT INTO source_file (SELECT *, id = type::record(string::replace(string(id), "file:", "source_file:")) FROM ` + "`file`" + `);
		
		-- 2. Update file_chunk references
		UPDATE file_chunk SET file = type::record(string::replace(string(file), "file:", "source_file:")) WHERE string::starts_with(string(file), "file:");
		
		-- 3. Update 'contains' edge (Repo -> File)
		UPDATE contains SET out = type::record(string::replace(string(out), "file:", "source_file:")) WHERE string::starts_with(string(out), "file:");
		
		-- 4. Update 'changed' edge (Commit -> File)
		UPDATE changed SET out = type::record(string::replace(string(out), "file:", "source_file:")) WHERE string::starts_with(string(out), "file:");
		
		-- 5. Update 'related_to' edge (ErrorType -> File)
		UPDATE related_to SET out = type::record(string::replace(string(out), "file:", "source_file:")) WHERE string::starts_with(string(out), "file:");
		
		-- 6. Clean up (Remove legacy records)
		DELETE ` + "`file`" + `;
		
		COMMIT;
	`

	_, err = db.Execute(ctx, migrationSQL)
	if err != nil {
		return fmt.Errorf("migration transaction failed: %w", err)
	}

	logger.Info("[MIGRATION] Successfully migrated %d records from 'file' to 'source_file'.", count)
	return nil
}

// Migrate handles database schema migrations.
func Migrate(ctx context.Context, dbClient Executor) error {
	logger.Info("Checking for database migrations...")

	// 1. Create migration table if not exists
	_, err := dbClient.Execute(ctx, "DEFINE TABLE migration SCHEMALESS;")
	if err != nil {
		return err
	}

	// 2. Check current version
	res, err := dbClient.Execute(ctx, "SELECT version FROM migration:current;")
	if err != nil {
		// If it doesn't exist, start from 0
		logger.Info("No migration history found. Initializing...")
	}

	currentVersion := 0
	if rows, ok := res.([]interface{}); ok && len(rows) > 0 {
		if row, ok := rows[0].(map[string]interface{}); ok {
			if v, ok := row["version"].(float64); ok {
				currentVersion = int(v)
			}
		}
	}

	logger.Info("Current DB Version: %d", currentVersion)

	// Define migrations
	migrations := []struct {
		ID  int
		SQL string
	}{
		{ID: 1, SQL: "UPDATE commit SET hash = string::trim(hash) WHERE hash != string::trim(hash);"},
		{ID: 2, SQL: `
			BEGIN TRANSACTION;
			UPDATE contains SET out = type::record(string(out));
			UPDATE changed SET out = type::record(string(out));
			UPDATE related_to SET out = type::record(string(out));
			UPDATE file_chunk SET file = type::record(string(file));
			COMMIT;
		`},
	}

	for _, m := range migrations {
		if m.ID > currentVersion {
			logger.Info("Applying migration %d...", m.ID)
			if _, err := dbClient.Execute(ctx, m.SQL); err != nil {
				return fmt.Errorf("failed migration %d: %w", m.ID, err)
			}
			// Update version
			updateQL := fmt.Sprintf("UPDATE migration:current SET version = %d, applied_at = time::now();", m.ID)
			if _, err := dbClient.Execute(ctx, updateQL); err != nil {
				// Fallback to CREATE if UPDATE failed
				createQL := fmt.Sprintf("CREATE migration:current SET version = %d, applied_at = time::now();", m.ID)
				dbClient.Execute(ctx, createQL)
			}
			currentVersion = m.ID
		}
	}

	return nil
}

// ClearAbsoluteFileRecords removes records that use absolute path IDs.
// This is called once during the transition to relative path indexing.
func ClearAbsoluteFileRecords(ctx context.Context, db Executor) error {
	logger.Info("[MIGRATION] Checking for legacy absolute-path file records...")

	// We detect absolute-path records by the LACK of 'rel_path' field.
	// In SurrealDB schemaless, we can check for field existence or just delete those where it's NONE.

	// Count records to delete
	res, err := db.Execute(ctx, "SELECT count() FROM source_file WHERE rel_path = NONE;")
	if err != nil {
		return fmt.Errorf("failed to check for absolute records: %w", err)
	}

	var count int64
	if rows, ok := res.([]interface{}); ok && len(rows) > 0 {
		if row, ok := rows[0].(map[string]interface{}); ok {
			if c, ok := row["count"].(float64); ok {
				count = int64(c)
			}
			if c, ok := row["count"].(int64); ok {
				count = c
			}
		}
	}

	if count == 0 {
		logger.Debug("[MIGRATION] No absolute-path records found.")
		return nil
	}

	logger.Info("[MIGRATION] Purging %d legacy absolute-path records to allow relative indexing...", count)

	cleanupSQL := `
		BEGIN TRANSACTION;
		DELETE file_chunk;
		DELETE source_file;
		COMMIT;
	`

	_, err = db.Execute(ctx, cleanupSQL)
	if err != nil {
		return fmt.Errorf("cleanup transaction failed: %w", err)
	}

	logger.Info("[MIGRATION] Successfully purged legacy records.")
	return nil
}
