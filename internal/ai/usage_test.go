package ai

import (
	"context"
	"strings"
	"testing"
)

type CapturingMockDB struct {
	LastSQL  string
	LastVars interface{}
}

func (m *CapturingMockDB) Execute(ctx context.Context, sql string) (interface{}, error) {
	m.LastSQL = sql
	return nil, nil
}

func (m *CapturingMockDB) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	m.LastSQL = sql
	m.LastVars = vars
	return nil, nil
}

func (m *CapturingMockDB) Close() {}

func TestWorkerUsageIDFormat(t *testing.T) {
	// Initialize a worker via newWorker (which calls updateUsageID)
	mockDB := &CapturingMockDB{}
	cfg := KeyConfig{
		Key:   "testkey",
		RPM:   10,
		TPM:   1000,
		RPD:   1000,
		Owner: "Test",
	}

	w := newWorker(context.Background(), cfg, mockDB)

	// Check the usageID field directly
	// It should look like "key_usage:hash_YYYYMMDD"
	// We specifically want to ensure it DOES NOT contain hyphens in the date part.

	// The hash is 8 chars hex.
	// "key_usage:" is 10 chars.
	// So it starts with "key_usage:".
	if !strings.HasPrefix(w.usageID, "key_usage:") {
		t.Fatalf("usageID expected to start with 'key_usage:', got %s", w.usageID)
	}

	// The part after "key_usage:" should be "hash_date"
	parts := strings.Split(w.usageID, ":")
	if len(parts) != 2 {
		t.Fatalf("Invalid usageID format: %s", w.usageID)
	}
	idPart := parts[1]

	// Split by "_" to separate hash and date
	// Note: If date contains hyphens, it might still split by underscore if format was hash_date
	// But we want to check if the date part has hyphens.

	idParts := strings.Split(idPart, "_")
	if len(idParts) != 2 {
		t.Fatalf("Expected ID to be hash_date, got %s", idPart)
	}

	datePart := idParts[1]

	// Check if datePart contains hyphens
	if strings.Contains(datePart, "-") {
		t.Errorf("Date part of usageID contains hyphens: %s. This causes SurrealDB parse errors.", datePart)
	}

	// Also verify that the generated SQL in updateDBUsage uses this ID
	w.updateDBUsage(context.Background(), w.usageID, 5, "Test")

	if !strings.Contains(mockDB.LastSQL, w.usageID) {
		t.Errorf("Expected SQL to contain usageID %s, got %s", w.usageID, mockDB.LastSQL)
	}

	// Ensure the SQL doesn't look like "UPDATE key_usage:hash_2026-02-02 ..." which is invalid
	// If usageID is safe, the SQL is safe (assuming no other weird injection).
	// We just re-confirm the ID safety.
}
