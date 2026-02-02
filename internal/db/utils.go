package db

import (
	"regexp"
	"strings"
)

var (
	// invalidIDChars matches any character that is NOT lowercase alphanumeric or underscore.
	invalidIDChars = regexp.MustCompile(`[^a-z0-9_]`)
	// multiUnderscore matches sequences of underscores to collapse them.
	multiUnderscore = regexp.MustCompile(`_+`)
)

// SanitizeID makes a string safe for use as a SurrealDB record ID suffix.
// It replaces characters that are illegal in unquoted identifiers (-, ., /, \, space, etc.) with underscores.
func SanitizeID(s string) string {
	// 1. Convert to lowercase
	s = strings.ToLower(s)

	// 2. Remove quotes completely (heuristic: they are often inside words)
	s = strings.ReplaceAll(s, "'", "")
	s = strings.ReplaceAll(s, "\"", "")
	s = strings.ReplaceAll(s, "`", "")

	// 3. Replace all other invalid characters with underscore
	// This covers /, \, ., -, :, {, }, =, >, space, etc.
	s = invalidIDChars.ReplaceAllString(s, "_")

	// 4. Collapse multiple underscores
	s = multiUnderscore.ReplaceAllString(s, "_")

	// 5. Trim leading/trailing underscores (optional but clean)
	s = strings.Trim(s, "_")

	return s
}

// EscapeSQL escapes a string for use in a SurrealDB single-quoted string literal.
func EscapeSQL(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}
