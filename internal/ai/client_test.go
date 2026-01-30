package ai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Accessing private fields for testing is allowed in same package

type MockDB struct{}
func (m *MockDB) Execute(sql string) (interface{}, error) { return nil, nil }
func (m *MockDB) SmartQuery(sql string, vars interface{}) (interface{}, error) { return nil, nil }
func (m *MockDB) Close() {}

func TestClientRotationAndFailover(t *testing.T) {
	// Mock Server
	// We want to simulate:
	// Key1 -> 429
	// Key2 -> 200
	var key1Attempts, key2Attempts int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ignore probe requests
		if r.Method == "GET" {
			w.WriteHeader(200)
			return
		}

		key := r.URL.Query().Get("key")
		if strings.Contains(key, "key1") {
			key1Attempts++
			w.WriteHeader(429)
			w.Write([]byte(`{
				"error": {
					"code": 429,
					"message": "Quota exceeded",
					"details": [
						{
							"@type": "type.googleapis.com/google.rpc.RetryInfo",
							"retryDelay": "2s"
						}
					]
				}
			}`))
			return
		}
		if strings.Contains(key, "key2") {
			key2Attempts++
			w.WriteHeader(200)
			w.Write([]byte(`{
				"embeddings": [
					{"values": [0.1, 0.2, 0.3]}
				]
			}`))
			return
		}
		w.WriteHeader(400) // Should not reach here
	}))
	defer server.Close()

	// Override BaseURL
	originalBaseURL := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = originalBaseURL }()

	// Create Client with 2 keys
	client := NewClient([]KeyConfig{
		{Key: "key1", RPM: 60, TPM: 1000, RPD: 100, Owner: "User1"},
		{Key: "key2", RPM: 60, TPM: 1000, RPD: 100, Owner: "User2"},
	}, &MockDB{})

	// Call
	// Should try key1 -> 429 -> mark key1 busy for 2s -> try key2 -> success
	embs, err := client.EmbedText("test")

	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}
	if len(embs) != 3 {
		t.Errorf("Expected 3 values, got %d", len(embs))
	}

	if key1Attempts != 1 {
		t.Errorf("Expected 1 attempt for key1, got %d", key1Attempts)
	}
	if key2Attempts != 1 {
		t.Errorf("Expected 1 attempt for key2, got %d", key2Attempts)
	}

	// Verify key1 is "busy"
	// client.workers[0] corresponds to key1 (assuming order preserved)
	w1 := client.workers[0]
	if time.Now().After(w1.nextAvailable) {
		t.Errorf("Expected worker 1 to be busy in the future")
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

	// RPM = 60 => 1 req/sec per worker if 1 key
	// We use 1 key.
	client := NewClient([]KeyConfig{
		{Key: "key1", RPM: 60},
	}, &MockDB{})

	start := time.Now()
	// 1st call: Should be immediate
	client.EmbedText("1")
	// 2nd call: Should wait 1s (cost=1)
	client.EmbedText("2")
	duration := time.Since(start)

	if duration < 900*time.Millisecond {
		t.Errorf("Expected pacing delay (~1s), but took %v", duration)
	}
}
