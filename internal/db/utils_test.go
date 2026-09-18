package db

import (
	"testing"
)

func TestSanitizeID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple.txt", "simple_txt"},
		{"path/to/file.go", "path_to_file_go"},
		{"weird char's", "weird_chars"}, // quotes removed, space to underscore
		{"file-with-dashes", "file_with_dashes"},
		// The failing case
		{"terenzif_documenti_{scadenzadocumento_aspx_=>_scadenzedocumento}", "terenzif_documenti_scadenzadocumento_aspx_scadenzedocumento"},
		// Windows path with multiple special chars
		{"c:\\Users\\Foo\\Code.cs", "c_users_foo_code_cs"},
		// Other potential issues
		{"key=value", "key_value"},
		{"pointer->value", "pointer_value"},
		{"__start_end__", "start_end"}, // trimming
	}

	for _, tt := range tests {
		got := SanitizeID(tt.input)
		if got != tt.expected {
			t.Errorf("SanitizeID(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEscapeSQL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple string", "simple string"},
		{"It's a test", "It\\'s a test"},
		{"C:\\Path\\To\\File", "C:\\\\Path\\\\To\\\\File"},
		{"Line 1\nLine 2", "Line 1\\nLine 2"},
		{"Mixed: 'C:\\test\n'", "Mixed: \\'C:\\\\test\\n\\'"},
	}

	for _, tt := range tests {
		got := EscapeSQL(tt.input)
		if got != tt.expected {
			t.Errorf("EscapeSQL(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFormatRecordID(t *testing.T) {
	tests := []struct {
		table    string
		id       string
		expected string
	}{
		{"source_file", "extensionmethods_cs", "source_file:⟨extensionmethods_cs⟩"},
		{"error_type", "abc", "error_type:⟨abc⟩"},
		{"", "table:id", "table:⟨id⟩"},
	}

	for _, tt := range tests {
		got := FormatRecordID(tt.table, tt.id)
		if got != tt.expected {
			t.Errorf("FormatRecordID(%q, %q) = %q; want %q", tt.table, tt.id, got, tt.expected)
		}
	}
}

func TestIsSafeRecordID(t *testing.T) {
	ok := []string{"commit:abc123", "file:⟨main_go⟩", "issue:42"}
	bad := []string{"", "nocolon", "x; DELETE repo;", "a:b c", "a:'b'", "DROP TABLE:x"}
	for _, id := range ok {
		if !IsSafeRecordID(id) {
			t.Errorf("IsSafeRecordID(%q) = false; want true", id)
		}
	}
	for _, id := range bad {
		if IsSafeRecordID(id) {
			t.Errorf("IsSafeRecordID(%q) = true; want false", id)
		}
	}
}

func TestCoerceRecordID(t *testing.T) {
	tests := []struct {
		name string
		raw  interface{}
		want string
	}{
		{"string", "file_chunk:abc", "file_chunk:abc"},
		{"map full", map[string]interface{}{"id": "file_chunk:abc"}, "file_chunk:abc"},
		{"map tb+id", map[string]interface{}{"tb": "file_chunk", "id": "abc"}, "file_chunk:abc"},
		{"map Table+ID", map[string]interface{}{"Table": "file_chunk", "ID": "abc"}, "file_chunk:abc"},
		{"map empty", map[string]interface{}{"other": "x"}, ""},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		if got := CoerceRecordID(tt.raw); got != tt.want {
			t.Errorf("%s: CoerceRecordID() = %q; want %q", tt.name, got, tt.want)
		}
	}
	// Surreal models.RecordID-like struct
	type recordID struct {
		Table string
		ID    string
	}
	got := CoerceRecordID(recordID{Table: "file_chunk", ID: "xyz"})
	if got != "file_chunk:xyz" {
		t.Errorf("struct RecordID: got %q; want file_chunk:xyz", got)
	}
	// Must not recurse infinitely on maps that miss id
	_ = CoerceRecordID(map[string]interface{}{"nested": map[string]interface{}{"id": "x"}})
}
