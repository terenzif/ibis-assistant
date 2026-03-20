package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type CountingMockDB struct {
	UpdateCount int64
}

func (m *CountingMockDB) Execute(ctx context.Context, sql string) (interface{}, error) {
	return nil, nil
}

func (m *CountingMockDB) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	atomic.AddInt64(&m.UpdateCount, 1)
	return nil, nil
}

func (m *CountingMockDB) Close() {}

func BenchmarkExcessiveDBUpdates(b *testing.B) {
	// Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{
			"embeddings": [
				{"values": [0.1, 0.2, 0.3]}
			]
		}`))
	}))
	defer server.Close()

	originalBaseURL := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = originalBaseURL }()

	mockDB := &CountingMockDB{}
	// High limits to avoid rate limiting during benchmark
	client := NewClient([]KeyConfig{
		{
			Key:           "benchKey",
			RPM:           100000,
			TPM:           1000000,
			RPD:           1000000,
			FlushInterval: 100 * time.Millisecond,
		},
	}, mockDB)

	ctx := context.Background()

	// Reset timer to ignore setup
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := client.EmbedText(ctx, "test")
		if err != nil {
			b.Fatalf("Embed failed: %v", err)
		}
	}

	// Allow async updates to propagate (at least one flush interval)
	time.Sleep(200 * time.Millisecond)

	updates := atomic.LoadInt64(&mockDB.UpdateCount)
	// We expect updates to be roughly (Duration / 100ms).
	// But calculating exact updates/op is what we want.
	b.ReportMetric(float64(updates)/float64(b.N), "db_updates/op")

	// Cleanup
	client.Stop()
}

func TestDBUpdateBatching(t *testing.T) {
	// Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{
			"embeddings": [
				{"values": [0.1, 0.2, 0.3]}
			]
		}`))
	}))
	defer server.Close()

	originalBaseURL := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = originalBaseURL }()

	mockDB := &CountingMockDB{}
	client := NewClient([]KeyConfig{
		{
			Key:           "testKey",
			RPM:           10000,
			TPM:           100000,
			RPD:           100000,
			FlushInterval: 100 * time.Millisecond,
		},
	}, mockDB)

	ctx := context.Background()
	count := 50

	for i := 0; i < count; i++ {
		_, err := client.EmbedText(ctx, "test")
		if err != nil {
			t.Fatalf("Embed failed: %v", err)
		}
	}

	// Wait for > 1 flush interval
	time.Sleep(250 * time.Millisecond)

	updates := atomic.LoadInt64(&mockDB.UpdateCount)
	t.Logf("Total requests: %d, DB Updates: %d", count, updates)

	// We expect DB updates to be significantly less than count
	// Ideally 1 or 2 (initial load? No, initial load is SELECT).
	// Initial load SELECTs. Update is UPDATE/CREATE.
	// We expect ~2-3 updates (depending on timing).

	if updates > 10 {
		t.Errorf("Too many DB updates! Got %d, expected < 10", updates)
	}

	client.Stop()
}
