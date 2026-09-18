package db

import (
	"encoding/json"
	"fmt"
	"reflect"
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
	// safeRecordIDTable matches a Surreal table name segment.
	safeRecordIDTable = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// safeRecordIDKey matches the ID segment (optionally angle-bracket wrapped).
	safeRecordIDKey = regexp.MustCompile(`^(?:⟨)?[a-zA-Z0-9_.-]+(?:⟩)?$`)
)

// IsSafeRecordID reports whether id is a single Surreal record reference safe to interpolate.
func IsSafeRecordID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, ";\n\r\t '\"\\") {
		return false
	}
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	return safeRecordIDTable.MatchString(parts[0]) && safeRecordIDKey.MatchString(parts[1])
}

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

// CoerceRecordID converts SurrealDB driver ID values (string, map, or RecordID struct) into a record ID string.
func CoerceRecordID(raw interface{}) string {
	if raw == nil {
		return ""
	}
	if s, ok := raw.(string); ok {
		return s
	}
	if m, ok := raw.(map[string]interface{}); ok {
		return coerceRecordIDMap(m)
	}
	if s, ok := raw.(fmt.Stringer); ok {
		if out := s.String(); out != "" && out != "<nil>" {
			return out
		}
	}

	if b, err := json.Marshal(raw); err == nil {
		var asString string
		if err := json.Unmarshal(b, &asString); err == nil && asString != "" {
			return asString
		}
		var asMap map[string]interface{}
		if err := json.Unmarshal(b, &asMap); err == nil && asMap != nil {
			if s := coerceRecordIDMap(asMap); s != "" {
				return s
			}
		}
	}

	rv := reflect.ValueOf(raw)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return ""
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct {
		var table, id string
		for i := 0; i < rv.NumField(); i++ {
			f := rv.Field(i)
			name := strings.ToLower(rv.Type().Field(i).Name)
			if !f.CanInterface() {
				continue
			}
			s, ok := f.Interface().(string)
			if !ok || s == "" {
				continue
			}
			switch name {
			case "table", "tb":
				table = s
			case "id", "key":
				id = s
			}
		}
		if table != "" && id != "" {
			if strings.Contains(id, ":") {
				return id
			}
			return table + ":" + id
		}
	}

	s := fmt.Sprint(raw)
	if s == "<nil>" {
		return ""
	}
	return s
}

func coerceRecordIDMap(v map[string]interface{}) string {
	idVal, _ := v["id"].(string)
	if idVal == "" {
		idVal, _ = v["ID"].(string)
	}
	if idVal == "" {
		return ""
	}
	if strings.Contains(idVal, ":") {
		return idVal
	}
	tb, _ := v["tb"].(string)
	if tb == "" {
		tb, _ = v["table"].(string)
	}
	if tb == "" {
		tb, _ = v["Table"].(string)
	}
	if tb != "" {
		return tb + ":" + idVal
	}
	return idVal
}
