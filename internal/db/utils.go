package db

import (
	"strings"
)

// SanitizeID makes a string safe for use as a SurrealDB record ID suffix.
// It replaces characters that are illegal in unquoted identifiers (-, ., /, \, space, etc.) with underscores.
func SanitizeID(s string) string {
	// Replace common separators and illegal chars with underscore
	safe := strings.ReplaceAll(s, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	safe = strings.ReplaceAll(safe, ".", "_")
	safe = strings.ReplaceAll(safe, "-", "_")
	safe = strings.ReplaceAll(safe, " ", "_")
	safe = strings.ReplaceAll(safe, ":", "_") // Colons are separators in Table:ID

	// Remove quotes completely
	safe = strings.ReplaceAll(safe, "'", "")
	safe = strings.ReplaceAll(safe, "\"", "")
	safe = strings.ReplaceAll(safe, "`", "")

	return strings.ToLower(safe)
}

// EscapeSQL escapes a string for use in a SurrealDB single-quoted string literal.
func EscapeSQL(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}
