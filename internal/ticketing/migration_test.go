package ticketing

import (
	"context"
	"strings"
	"testing"
)

type migrationMockDB struct {
	queries []string
}

func (m *migrationMockDB) Execute(ctx context.Context, sql string) (interface{}, error) {
	m.queries = append(m.queries, sql)
	return nil, nil
}

func (m *migrationMockDB) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	return nil, nil
}

func (m *migrationMockDB) Close() {}

func TestMigrateLegacyIssues(t *testing.T) {
	mock := &migrationMockDB{}
	if err := MigrateLegacyIssues(context.Background(), mock); err != nil {
		t.Fatalf("MigrateLegacyIssues failed: %v", err)
	}
	if len(mock.queries) != 1 {
		t.Fatalf("expected one migration query, got %d", len(mock.queries))
	}
	query := strings.ToLower(mock.queries[0])
	if !strings.Contains(query, "provider = 'redmine'") {
		t.Fatalf("expected provider backfill query, got %s", mock.queries[0])
	}
}
