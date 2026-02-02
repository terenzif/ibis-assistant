package db

import "testing"

func TestSanitizeID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"Space Name", "space_name"},
		{"Jean-Luc", "jean_luc"},
		{"file.txt", "file_txt"},
		{"path/to/file", "path_to_file"},
		{"win\\path", "win_path"},
		{"O'Connor", "oconnor"},
		{"Quote\"Check", "quotecheck"},
		{"2026-02-02", "2026_02_02"},
	}

	for _, test := range tests {
		got := SanitizeID(test.input)
		if got != test.expected {
			t.Errorf("SanitizeID(%q) = %q; want %q", test.input, got, test.expected)
		}
	}
}

func TestEscapeSQL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal", "normal"},
		{"it's me", "it\\'s me"},
		{"line\nbreak", "line\\nbreak"},
		{"back\\slash", "back\\\\slash"},
	}

	for _, test := range tests {
		got := EscapeSQL(test.input)
		if got != test.expected {
			t.Errorf("EscapeSQL(%q) = %q; want %q", test.input, got, test.expected)
		}
	}
}
