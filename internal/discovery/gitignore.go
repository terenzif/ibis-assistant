package discovery

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// IgnoreMatcher manages .gitignore rules
type IgnoreMatcher struct {
	patterns []string
}

// NewIgnoreMatcher creates a matcher from a .gitignore file
func NewIgnoreMatcher(path string) (*IgnoreMatcher, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var patterns []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Basic normalization
		patterns = append(patterns, line)
	}

	return &IgnoreMatcher{patterns: patterns}, scanner.Err()
}

// Match checks if a path matches any ignore pattern
// This is a simplified implementation. For full gitignore compliance,
// a dedicated library would be better, but this suffices for "noise filtering".
func (m *IgnoreMatcher) Match(path string, isDir bool) bool {
	name := filepath.Base(path)
	for _, p := range m.patterns {
		// Handle directory-specific patterns (ending with /)
		onlyDir := strings.HasSuffix(p, "/")
		cleanP := strings.TrimSuffix(p, "/")

		if onlyDir && !isDir {
			continue
		}

		// Exact match (e.g. "node_modules")
		if cleanP == name {
			return true
		}

		// Glob match (e.g. "*.log")
		matched, _ := filepath.Match(cleanP, name)
		if matched {
			return true
		}
		
		// Handle path-based matches relative to where .gitignore was found is tricky
		// without passing context. For now, we assume patterns match filenames globally
		// or at the level of the scan.
	}
	return false
}
