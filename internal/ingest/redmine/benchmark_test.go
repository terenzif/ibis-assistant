package redmine

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// SimulatedLatencyDB implements db.Executor with a fixed latency
type SimulatedLatencyDB struct {
	Latency time.Duration
}

func (m *SimulatedLatencyDB) Execute(ctx context.Context, sql string) (interface{}, error) {
	time.Sleep(m.Latency)
	return nil, nil
}

func (m *SimulatedLatencyDB) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	time.Sleep(m.Latency)
	return nil, nil
}

func (m *SimulatedLatencyDB) Close() {}

func BenchmarkIngestIssue(b *testing.B) {
	// Setup Client with a mock HTTP client to avoid network calls
	mockHTTP := &MockHTTPClient{
		Response: `{"issue": {"id": 123, "tracker": {"id": 1, "name": "Bug"}, "author": {"id": 2, "name": "Dave"}, "status": {"id": 3, "name": "New"}, "subject": "Test", "description": "Desc"}}`,
	}

	client := NewClient("http://mock-redmine", "key")
	client.HTTP = mockHTTP.Client()

	// Simulate 5ms round-trip latency to DB
	// With 5 separate calls, we expect approx 25ms per Op + overhead.
	mockDB := &SimulatedLatencyDB{Latency: 5 * time.Millisecond}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := client.IngestIssue(ctx, mockDB, "123")
		if err != nil {
			b.Fatalf("IngestIssue failed: %v", err)
		}
	}
}

// MockHTTPClient helper
type MockHTTPClient struct {
	Response string
}

func (m *MockHTTPClient) Client() *http.Client {
	return &http.Client{
		Transport: &mockTransport{
			Response: m.Response,
		},
	}
}

type mockTransport struct {
	Response string
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(m.Response)),
		Header:     make(http.Header),
	}, nil
}
