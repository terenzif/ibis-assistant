package code

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// loadIgnorePatterns reads .gitignore and .knowledgeignore
func loadIgnorePatterns(repoPath string) ([]string, error) {
	var patterns []string
	// Order: .gitignore first, then .knowledgeignore (so knowledgeignore can add more, or conceptually "override" if we had negation)
	// Actually order doesn't matter for union.
	files := []string{".gitignore", ".knowledgeignore"}

	for _, name := range files {
		f, err := os.Open(filepath.Join(repoPath, name))
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			patterns = append(patterns, line)
		}
		f.Close()
	}
	return patterns, nil
}

// isIgnored checks if a path matches any pattern.
// path is the absolute path to the file/dir.
// root is the repo root.
// isDir indicates if the path is a directory.
func isIgnored(path string, root string, patterns []string, isDir bool) bool {
	if len(patterns) == 0 {
		return false
	}

	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	// Normalize to forward slashes for matching
	rel = filepath.ToSlash(rel)
	name := filepath.Base(path)

	for _, p := range patterns {
		// handle directory marker
		dirOnly := strings.HasSuffix(p, "/")
		cleanP := strings.TrimSuffix(p, "/")

		if dirOnly && !isDir {
			// If pattern implies directory (ends in /), but this is a file,
			// it only matches if this file is INSIDE that directory.
			// But isIgnored is called for the directory itself too.
			// If we are checking a file "foo/bar.txt" against "foo/",
			// we rely on the fact that we skipped "foo" dir?
			// No, PruneRepo checks files directly.
			// So if p="foo/", and path="foo/bar.txt", it should match.
		}

		// 1. Name match (unanchored)
		// e.g. "*.log", "node_modules"
		if !strings.Contains(cleanP, "/") {
			if matched, _ := filepath.Match(cleanP, name); matched {
				if dirOnly && !isDir {
					continue // Pattern "foo/" doesn't match file "foo"
				}
				return true
			}
		}

		// 2. Path match
		// Handle leading slash (anchored to root)
		if strings.HasPrefix(cleanP, "/") {
			// Absolute in repo
			target := strings.TrimPrefix(cleanP, "/")
			if matched, _ := filepath.Match(target, rel); matched {
				if dirOnly && !isDir {
					continue
				}
				return true
			}
			// Prefix check for directory match
			if strings.HasPrefix(rel, target+"/") {
				return true
			}
		} else {
			// Unanchored path? e.g. "src/foo" matches "src/foo" but also "a/src/foo"?
			// gitignore says: if pattern contains slash (other than trailing), it is relative to .gitignore location (root here).
			// So "src/foo" means "/src/foo".
			if strings.Contains(cleanP, "/") {
				if matched, _ := filepath.Match(cleanP, rel); matched {
					if dirOnly && !isDir {
						continue
					}
					return true
				}
				if strings.HasPrefix(rel, cleanP+"/") {
					return true
				}
			}
		}

		// 3. Recursive directory match for unanchored dirs
		// e.g. "node_modules/" matches "src/node_modules/"
		if dirOnly && !strings.HasPrefix(p, "/") && !strings.Contains(cleanP, "/") {
			// pattern is "foo/"
			// check if rel contains "/foo/" or starts with "foo/"
			if strings.HasPrefix(rel, cleanP+"/") || strings.Contains(rel, "/"+cleanP+"/") {
				return true
			}
		}
	}
	return false
}
