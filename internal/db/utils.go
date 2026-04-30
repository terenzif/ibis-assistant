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
	// isNumericId detects IDs that are purely numeric, which don't need bracket wrapping.
	isNumericId = regexp.MustCompile(`^[0-9]+$`)
)

// FormatRecordID returns a SurrealDB record ID formatted with angled brackets for v3.0 compatibility.
func FormatRecordID(table, id string) string {
	if strings.Contains(id, ":") {
		// If it's already a complete record ID, parse it and wrap only the ID part to be safe.
		parts := strings.SplitN(id, ":", 2)
		if len(parts) == 2 {
			tablePart := parts[0]
			idPart := parts[1]
			// Don't re-wrap if already wrapped
			if strings.HasPrefix(idPart, "⟨") && strings.HasSuffix(idPart, "⟩") {
				return id
			}
			if isNumericId.MatchString(idPart) && tablePart == "issue" {
				return tablePart + ":" + idPart
			}
			return tablePart + ":⟨" + idPart + "⟩"
		}
		return id
	}
	if isNumericId.MatchString(id) && table == "issue" {
		return table + ":" + id
	}
	return table + ":⟨" + id + "⟩"
}

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
