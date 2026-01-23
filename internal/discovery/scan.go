package discovery

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Scanner handles the repository discovery process
type Scanner struct {
	Root     string
	Excludes []string
}

// NewScanner creates a new Scanner instance
func NewScanner(root string) *Scanner {
	return &Scanner{
		Root: root,
		
		// Default hardcoded excludes for safety, can be extended via config later
		Excludes: []string{
			"node_modules",
			"vendor",
			".git", // We look FOR .git, but we don't scan INSIDE .git for other .git
			"dist",
			"build",
			"bin",
			"obj",
			".vs",
			".idea",
			".vscode",
		},
	}
}

// Scan walks the directory tree to find Git repositories
func (s *Scanner) Scan() ([]string, error) {
	var repos []string

	// Check if Root is itself a git repo
	if isGitRepo(s.Root) {
		repos = append(repos, s.Root)
		subs, err := scanSubmodules(s.Root)
		if err == nil {
			repos = append(repos, subs...)
		}
		return repos, nil
	}

	// Try to load root .gitignore
	var rootMatcher *IgnoreMatcher
	if m, err := NewIgnoreMatcher(filepath.Join(s.Root, ".gitignore")); err == nil {
		rootMatcher = m
	}

	err := filepath.WalkDir(s.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if !d.IsDir() {
			return nil
		}

		name := d.Name()
		
		// Check hardcoded excludes
		for _, exc := range s.Excludes {
			if strings.EqualFold(name, exc) {
				return filepath.SkipDir
			}
		}

		// Check root .gitignore
		if rootMatcher != nil {
			// We match against relative path or name?
			// gitignore usually matches patterns. Simpler to match Name for basic excludes.
			// Or check relative path.
			// Matcher struct I wrote assumes name or simple glob.
			if rootMatcher.Match(name, true) {
				return filepath.SkipDir
			}
		}

		// If this is a git repo
		if isGitRepo(path) {
			repos = append(repos, path)
			
			// Check for submodules
			subs, _ := scanSubmodules(path)
			repos = append(repos, subs...)
			
			// Don't recurse INSIDE a git repo to find other git repos 
			return filepath.SkipDir 
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return repos, nil
}

func isGitRepo(path string) bool {
	gitDir := filepath.Join(path, ".git")
	_, err := os.Stat(gitDir)
	return err == nil
}

func scanSubmodules(repoRoot string) ([]string, error) {
	gitmodulesPath := filepath.Join(repoRoot, ".gitmodules")
	f, err := os.Open(gitmodulesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var submodules []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "path =") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				relPath := strings.TrimSpace(parts[1])
				absPath := filepath.Join(repoRoot, relPath)
				if _, err := os.Stat(absPath); err == nil {
					submodules = append(submodules, absPath)
				}
			}
		}
	}
	return submodules, nil
}
