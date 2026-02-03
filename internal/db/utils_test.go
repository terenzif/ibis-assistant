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
		{"deckonline_documenti_{scadenzadocumento_aspx_=>_scadenzedocumento}", "deckonline_documenti_scadenzadocumento_aspx_scadenzedocumento"},
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
