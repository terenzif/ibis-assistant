package code

import (
	"strings"
	"testing"
)

func TestChunkContent(t *testing.T) {
	// 50 chars total
	// 1234567890\n1234567890\n...
	text := "line1\nline2\nline3\nline4\nline5"
	// 5 chars + 1 newline = 6 chars per line.
	// 5 lines * 6 = 30 chars.

	// Test case 1: infinite size -> 1 chunk
	chunks, err := chunkContent(strings.NewReader(text), 100)
	if err != nil {
		t.Fatalf("Error chunking: %v", err)
	}
	if len(chunks) != 1 {
		t.Errorf("Expected 1 chunk, got %d", len(chunks))
	}
	// My new implementation appends newline after every line read by scanner.
	// text is "line1\n...line5"
	// output is "line1\n...line5\n"
	if len(chunks) > 0 && chunks[0] != text+"\n" {
		t.Errorf("Expected '%s', got '%s'", text+"\n", chunks[0])
	}

	// Test case 2: Small size (e.g. 7 chars -> 1 line per chunk)
	// chunk content logic: if current + len > size -> flush.
	// "line1\n" len is 6.
	// If max is 10. "line1\n" (6) ok. "line2\n" (6). 6+6=12 > 10. Flush "line1\n". Start "line2\n".
	chunks, err = chunkContent(strings.NewReader(text), 10)
	if err != nil {
		t.Fatalf("Error chunking: %v", err)
	}
	if len(chunks) != 5 {
		t.Errorf("Expected 5 chunks, got %d (Chunks: %v)", len(chunks), chunks)
	}
	if len(chunks) > 0 && chunks[0] != "line1\n" {
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
