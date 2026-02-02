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

func TestTPMTokenBucket(t *testing.T) {
	// This test verifies that we throttle based on TPM

	// Create a worker manually to test waitRateLimits directly if possible,
	// or use NewClient with mocks.

	// Let's use a very low TPM limit.
	// TPM = 600 => 10 tokens / second.
	// We want to send a text that is estimated to be ~20 tokens.
	// It should take ~2 seconds to refill.

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"embeddings": [{"values": [0.1]}]}`))
	}))
	defer server.Close()

	originalBaseURL := BaseURL
	BaseURL = server.URL
	defer func() { BaseURL = originalBaseURL }()

	// We'll rely on the estimation logic which we plan to change to len/3.
	// 60 chars -> 20 tokens.
	text60 := strings.Repeat("a", 60)

	client := NewClient([]KeyConfig{
		{Key: "keyTPM", RPM: 600, TPM: 600}, // RPM is high (10/s), TPM is limiter (10 tok/s)
	}, &MockDB{})

	// Allow the bucket to fill (it starts full usually).
	// Current impl: Fixed window. Starts at 0 used.
	// Proposed impl: Bucket starts full (maxBucket).

	// 1. First Request: 60 chars ~ 20 tokens.
	// Should pass immediately (burst).
	start := time.Now()
	_, err := client.EmbedText(text60)
	if err != nil {
		t.Fatalf("First request failed: %v", err)
	}
	dur1 := time.Since(start)
	if dur1 > 500*time.Millisecond {
		t.Logf("First request took long: %v (Expected fast burst)", dur1)
	}

	// 2. Burst depletion.
	// Limit is 600 TPM -> 10/sec.
	// If maxBucket is 600, we can burst 600 tokens.
	// That's 30 requests of 20 tokens.
	// This test might be tricky if we set maxBucket = limitTPM.

	// Let's force a wait by using a smaller TPM limit or larger request.
	// TPM = 60 -> 1 token/sec.
	// Text = 60 chars -> 20 tokens.
	// Limit 60 TPM.
	// 1. Req (20 tokens). Burst OK.
	// 2. Req (20 tokens). Burst OK.
	// 3. Req (20 tokens). Burst OK.
	// 4. Req (20 tokens). Empty?
	// If maxBucket = 60, we can do 3 reqs. 4th should block for ~20s?

	// Let's try TPM = 120 (2 tokens/sec).
	// Text = 60 chars (~20 tokens).
	// MaxBucket = 120.
	// We can do 6 requests immediately.
	// Then we are blocked.

	// We use TPM = 134.
	// Internal logic applies 0.9 safety factor -> Limit = ~120.6.
	// Refill Rate = 120.6 / 60 = 2.01 tok/sec.
	client2 := NewClient([]KeyConfig{
		{Key: "keyLowTPM", RPM: 1000, TPM: 134},
	}, &MockDB{})

	// Helper to eat tokens
	eatTokens := func(n int) {
		for i := 0; i < n; i++ {
			_, err := client2.EmbedText(text60)
			if err != nil {
				t.Errorf("Eat request %d failed: %v", i, err)
			}
		}
	}

	// Eat 6 requests.
	// New estimation: 60 chars / 3 = 20 + 1 = 21 tokens per req.
	// 6 * 21 = 126 tokens.
	// Limit (safe) is ~120.6.
	// Excess = 126 - 120.6 = 5.4 tokens.
	// Wait time = 5.4 / 2.01 = ~2.7 seconds.

	startEat := time.Now()
	eatTokens(6)
	durEat := time.Since(startEat)
	t.Logf("Consumed 6 requests in %v", durEat)

	// With Fixed Window (current), this should take ~60 seconds (waiting for reset).
	// With Token Bucket (future), this should take ~4 seconds.

	if durEat < 2*time.Second {
		t.Errorf("Rate limit failed! Should have waited > 2s, but took %v", durEat)
	}
}
