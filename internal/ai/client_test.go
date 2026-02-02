package ai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Accessing private fields for testing is allowed in same package

type MockDB struct{}
func (m *MockDB) Execute(sql string) (interface{}, error) { return nil, nil }
func (m *MockDB) SmartQuery(sql string, vars interface{}) (interface{}, error) { return nil, nil }
func (m *MockDB) Close() {}

func TestClientWorkerPool(t *testing.T) {
	// Mock Server
	// Simulate 2 keys. Both working.
	var key1Count, key2Count int
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		mu.Lock()
		if strings.Contains(key, "key1") {
			key1Count++
		}
		if strings.Contains(key, "key2") {
			key2Count++
		}
		mu.Unlock()

		// Simulate latency
		time.Sleep(50 * time.Millisecond)

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

	// Create Client with 2 keys
	client := NewClient([]KeyConfig{
		{Key: "key1", RPM: 600, TPM: 1000, RPD: 1000},
		{Key: "key2", RPM: 600, TPM: 1000, RPD: 1000},
	}, &MockDB{})

	// Submit 10 requests
	count := 10
	var wg sync.WaitGroup
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			_, err := client.EmbedText("test")
			if err != nil {
				t.Errorf("Embed failed: %v", err)
			}
		}()
	}

	wg.Wait()

	mu.Lock()
	total := key1Count + key2Count
	mu.Unlock()

	if total != count {
		t.Errorf("Expected %d requests, got %d", count, total)
	}

	if key1Count == 0 || key2Count == 0 {
		t.Logf("Warning: One key did all work (key1: %d, key2: %d). Workers might not be competing fairly due to low load.", key1Count, key2Count)
	}
}

func TestClientRateLimitingPacing(t *testing.T) {
	// Mock Server always success
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"embeddings": [{"values": [0.1]}]}`))
	}))
	defer server.Close()

	originalBaseURL := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = originalBaseURL }()

	// RPM = 60 => 1 req/sec per worker
	client := NewClient([]KeyConfig{
		{Key: "key1", RPM: 60},
	}, &MockDB{})

	start := time.Now()
	// 1st call: Should be immediate (or very fast)
	client.EmbedText("1")
	// 2nd call: Should wait ~1s
	client.EmbedText("2")
	duration := time.Since(start)

	if duration < 900*time.Millisecond {
		t.Errorf("Expected pacing delay (~1s), but took %v", duration)
	}
}
