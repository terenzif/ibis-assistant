package code

import (
	"strings"
	"testing"
)

func BenchmarkChunkContent(b *testing.B) {
	// Create a large text input
	line := "This is a sample line of text to simulate code content."
	var sb strings.Builder
	// 1000 lines
	for i := 0; i < 1000; i++ {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	text := sb.String()
	chunkSize := 100 // Small chunk size to force many chunks and splits

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chunkContent(text, chunkSize)
	}
}
