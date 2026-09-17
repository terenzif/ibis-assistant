package schema_test

import (
	"testing"

	"github.com/terenzif/ibis-server/internal/schema"
)

func BenchmarkGenerateInitSQL(b *testing.B) {
	// Reset/Restore Definition after benchmark
	originalDefinition := schema.Definition
	defer func() { schema.Definition = originalDefinition }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = schema.GenerateInitSQL()
	}
}

func BenchmarkGenerateInitSQL_Large(b *testing.B) {
	// Save original definition
	originalDefinition := schema.Definition
	defer func() { schema.Definition = originalDefinition }()

	// Create a large definition slice (1000 items)
	largeDef := make([]string, 1000)
	for i := 0; i < 1000; i++ {
		largeDef[i] = "DEFINE INDEX test_index ON TABLE test_table COLUMNS col1, col2 UNIQUE;"
	}
	schema.Definition = largeDef

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = schema.GenerateInitSQL()
	}
}
