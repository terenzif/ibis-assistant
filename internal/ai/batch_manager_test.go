package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/terenzif/ibis-arc/internal/schema"
)

type MockDBBatch struct {
	LastSQL string
	Queries []string
	Rows    interface{}
}

func (m *MockDBBatch) Execute(ctx context.Context, sql string) (interface{}, error) {
	m.LastSQL = sql
	m.Queries = append(m.Queries, sql)
	return m.Rows, nil
}

func (m *MockDBBatch) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	m.LastSQL = sql
	return nil, nil
}

func (m *MockDBBatch) Close() {}

// MockAIClient implements a mock AI client for testing
type MockAIClient struct {
}

func (m *MockAIClient) IsFunctional() bool {
	return true
}

func (m *MockAIClient) BatchEmbedText(ctx context.Context, texts []string) ([][]float32, error) {
	// Return mock embeddings
	embeddings := make([][]float32, len(texts))
	for i := range texts {
		embeddings[i] = make([]float32, 768)
		for j := range embeddings[i] {
			embeddings[i][j] = float32(j) / 768.0
		}
	}
	return embeddings, nil
}

// createTestClient creates a minimal Client for testing that has IsFunctional() returning true
// and BatchEmbedText that returns mock embeddings. It also starts a goroutine that handles
// embed jobs by returning mock embeddings.
func createTestClient() *Client {
	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{
		jobQueue:      make(chan EmbedJob, 10),
		generateQueue: make(chan GenerateJob, 10),
		ctx:           ctx,
		cancel:        cancel,
	}

	// Start a goroutine that handles embedding jobs for testing
	go func() {
		for {
			select {
			case job := <-client.jobQueue:
				// Respond with mock embeddings
				embeddings := make([][]float32, len(job.Texts))
				for i := range job.Texts {
					embeddings[i] = make([]float32, 768)
					for j := range embeddings[i] {
						embeddings[i][j] = float32(j) / 768.0
					}
				}
				job.ResultChan <- EmbedResult{Embeddings: embeddings}
			case <-ctx.Done():
				return
			}
		}
	}()

	return client
}

func TestBatchManager_ProcessPendingChunks_Parsing(t *testing.T) {
	// Test cases for different SurrealDB response formats
	testCases := []struct {
		name     string
		rows     interface{}
		expected int // Number of chunks found
	}{
		{
			name: "Standard string ID",
			rows: []interface{}{
				map[string]interface{}{"id": "file_chunk:1", "content": "text1"},
				map[string]interface{}{"id": "file_chunk:2", "content": "text2"},
			},
			expected: 2,
		},
		{
			name: "SurrealDB v3 RecordID object",
			rows: []interface{}{
				map[string]interface{}{
					"id":      map[string]interface{}{"id": "file_chunk:3"},
					"content": "text3",
				},
			},
			expected: 1,
		},
		{
			name: "Mixed formats",
			rows: []interface{}{
				map[string]interface{}{"id": "file_chunk:4", "content": "text4"},
				map[string]interface{}{
					"id":      map[string]interface{}{"id": "file_chunk:5"},
					"content": "text5",
				},
			},
			expected: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			db := &MockDBBatch{Rows: tc.rows}
			// Create a minimal Client for testing
			client := createTestClient()
			bm := NewBatchManager(db, client, ".", 8000)

			// We can't easily check the internal state of chunkIDs without exposing it or using a hook.
			// But we can check if it attempted to call Execute for the selecting pending chunks.
			bm.processPendingChunks()

			foundFileChunkQuery := false
			for _, q := range db.Queries {
				if strings.Contains(q, schema.TableFileChunk) && strings.Contains(q, "batch_status = 'pending'") {
					foundFileChunkQuery = true
					break
				}
			}

			if !foundFileChunkQuery {
				t.Errorf("Expected query on %s with pending filter, queries were: %v", schema.TableFileChunk, db.Queries)
			}
		})
	}
}

func TestBatchManager_SQLQuoting(t *testing.T) {
	db := &MockDBBatch{
		Rows: []interface{}{
			map[string]interface{}{"id": "file_chunk:with'quote", "content": "text"},
		},
	}

	client := createTestClient()
	bm := NewBatchManager(db, client, ".", 8000)
	// This test is mostly a place holder for now since mocking AI.CreateBatchEmbedJob is hard without an interface.
	// But we've ensured the MockDB supports context.
	bm.processPendingChunks()
}
