package ai

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Accessing private fields for testing is allowed in same package
func TestClientRotation(t *testing.T) {
	keys := []string{"key1", "key2", "key3"}
	client := NewClient(keys, 60)

	// Check workers created
	if len(client.workers) != 3 {
		t.Fatalf("Expected 3 workers, got %d", len(client.workers))
	}

	// Verify Round Robin
	// 1st call -> index 1 (atomic add returns new value) % 3 -> 1%3 = 1?
	// atomic.AddUint32 starts at 0. Add(1) -> 1.
	// 1 % 3 = 1. So it starts at index 1 (key2).
	// Let's check the logic in gemini.go:
	// idx := atomic.AddUint32(&c.next, 1)
	// return c.workers[idx%uint32(len(c.workers))]
	
	w1 := client.getWorker() // idx=1 -> keys[1] ("key2")
	w2 := client.getWorker() // idx=2 -> keys[2] ("key3")
	w3 := client.getWorker() // idx=3 -> keys[0] ("key1")
	w4 := client.getWorker() // idx=4 -> keys[1] ("key2")

	if w1.apiKey != "key2" {
		t.Errorf("First worker should be key2, got %s", w1.apiKey)
	}
	if w2.apiKey != "key3" {
		t.Errorf("Second worker should be key3, got %s", w2.apiKey)
	}
	if w3.apiKey != "key1" {
		t.Errorf("Third worker should be key1, got %s", w3.apiKey)
	}
	if w4.apiKey != "key2" {
		t.Errorf("Fourth worker should be key2, got %s", w4.apiKey)
	}
}

func TestClientRetryOn429(t *testing.T) {
	// Mock Server
	var attempt int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			// First attempt: 429
			w.WriteHeader(429)
			w.Write([]byte(`{
				"error": {
					"code": 429,
					"message": "Quota exceeded",
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{
							"@type": "type.googleapis.com/google.rpc.RetryInfo",
							"retryDelay": "0.1s"
						}
					]
				}
			}`))
			return
		}
		// Second attempt: Success
		w.WriteHeader(200)
		w.Write([]byte(`{
			"embeddings": [
				{"values": [0.1, 0.2, 0.3]}
			]
		}`))
	}))
	defer server.Close()

	// Override BaseURL
	originalBaseURL := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = originalBaseURL }()

	// Create Client
	client := NewClient([]string{"test-key"}, 1000) // High RPM to avoid ticker delay

	// Call
	start := time.Now()
	embs, err := client.EmbedText("test")
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}
	if len(embs) != 3 {
		t.Errorf("Expected 3 values, got %d", len(embs))
	}
	if attempt != 2 {
		t.Errorf("Expected 2 attempts (1 failure + 1 success), got %d", attempt)
	}

	// Check if we waited at least 0.1s
	if duration < 100*time.Millisecond {
		t.Errorf("Expected delay of at least 100ms, took %v", duration)
	}
}
