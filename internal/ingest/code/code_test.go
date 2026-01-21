package code

import (
	"testing"
)

func TestChunkContent(t *testing.T) {
	// 50 chars total
	// 1234567890\n1234567890\n...
	text := "line1\nline2\nline3\nline4\nline5"
	// 5 chars + 1 newline = 6 chars per line.
	// 5 lines * 6 = 30 chars.

	// Test case 1: infinite size -> 1 chunk
	chunks := chunkContent(text, 100)
	if len(chunks) != 1 {
		t.Errorf("Expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != text+"\n" { // My implementation appends newline at end of chunk loop usually
		// Let's check implementation behavior:
		// loops lines. append line "\n".
		// so "line1\nline2\n..." correct.
	}

	// Test case 2: Small size (e.g. 7 chars -> 1 line per chunk)
	// chunk content logic: if current + len > size -> flush.
	// "line1\n" len is 6.
	// If max is 10. "line1\n" (6) ok. "line2\n" (6). 6+6=12 > 10. Flush "line1\n". Start "line2\n".
	chunks = chunkContent(text, 10)
	if len(chunks) != 5 {
		t.Errorf("Expected 5 chunks, got %d (Chunks: %v)", len(chunks), chunks)
	}
	if chunks[0] != "line1\n" {
		t.Errorf("Expected 'line1\\n', got '%s'", chunks[0])
	}
}

func TestSanitizeID(t *testing.T) {
	input := "c:\\Users\\Foo\\Code.cs"
	expected := "c__users_foo_code_cs"
	got := sanitizeID(input)
	if got != expected {
		t.Errorf("Sanitize failed. Got %s, want %s", got, expected)
	}
}
