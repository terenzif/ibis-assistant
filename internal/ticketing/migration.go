package ticketing

import (
	"context"
	"fmt"

	"github.com/terenzif/ibis-arc/internal/db"
	"github.com/terenzif/ibis-arc/internal/schema"
)

// MigrateLegacyIssues backfills provider metadata for pre-ticketing issue records.
func MigrateLegacyIssues(ctx context.Context, client db.Executor) error {
	if client == nil {
		return nil
	}
	ql := fmt.Sprintf("UPDATE %s SET provider = 'redmine', external_id = id, external_key = id WHERE provider = NONE OR provider = '';", schema.TableIssue)
	if _, err := client.Execute(ctx, ql); err != nil {
		return err
	}
	return nil
}
