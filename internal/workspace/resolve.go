package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/terenzif/ibis-assistant/internal/config"
)

// Resolved is a project workspace: either a server-owned clone or a live working tree.
type Resolved struct {
	Name  string
	Path  string
	Owned bool // true: discovery_root/dynamic clone; may clone/reset/fetch
}

// OwnedClonePath is the server-owned checkout under discovery_root/dynamic/<name>.
func OwnedClonePath(cfg *config.Config, repoName string) string {
	root := "."
	if cfg != nil && strings.TrimSpace(cfg.DiscoveryRoot) != "" {
		root = cfg.DiscoveryRoot
	}
	return filepath.Join(root, "dynamic", repoName)
}

// Resolve returns the workspace for repoName.
// Empty or server runtime_mode: owned clone path (caller may clone).
// personal/plugin: live path from projects[], git_repos, or cwd/IBIS_WORKSPACE; never clones.
func Resolve(cfg *config.Config, repoName string) (Resolved, error) {
	if cfg == nil {
		return Resolved{}, fmt.Errorf("config is required")
	}
	name := strings.TrimSpace(repoName)
	if !cfg.LiveWorkspace() {
		if name == "" {
			return Resolved{}, fmt.Errorf("project name is required")
		}
		return Resolved{Name: name, Path: OwnedClonePath(cfg, name), Owned: true}, nil
	}

	if p := findProject(cfg, name); p != nil && strings.TrimSpace(p.WorkingRepoPath) != "" {
		path := p.WorkingRepoPath
		if err := requireDir(path); err != nil {
			return Resolved{}, fmt.Errorf("working tree for %q not found at %s (personal/plugin mode does not clone): %w", displayName(name, path), path, err)
		}
		return Resolved{Name: displayName(name, path), Path: path, Owned: false}, nil
	}

	if path, ok := matchGitRepos(cfg, name); ok {
		if err := requireDir(path); err != nil {
			return Resolved{}, fmt.Errorf("working tree for %q not found at %s (personal/plugin mode does not clone): %w", displayName(name, path), path, err)
		}
		return Resolved{Name: displayName(name, path), Path: path, Owned: false}, nil
	}

	start := workspaceStart(cfg)
	if start != "" {
		root, ok := FindGitRoot(start)
		if ok {
			base := filepath.Base(root)
			if cfg.IsPlugin() || name == "" || strings.EqualFold(base, name) {
				return Resolved{Name: displayName(name, root), Path: root, Owned: false}, nil
			}
		} else if cfg.IsPlugin() {
			if err := requireDir(start); err != nil {
				return Resolved{}, fmt.Errorf("plugin workspace %s not found: %w", start, err)
			}
			return Resolved{Name: displayName(name, start), Path: start, Owned: false}, nil
		}
	}

	label := name
	if label == "" {
		label = "(unnamed)"
	}
	return Resolved{}, fmt.Errorf("no live workspace for %s: set projects[].working_repo_path or git_repos, or run from a git checkout (personal/plugin mode does not clone)", label)
}

// CandidatePaths lists directories to search for source when enriching logs.
func CandidatePaths(cfg *config.Config, repoName string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		key := filepath.Clean(p)
		if _, ok := seen[strings.ToLower(key)]; ok {
			return
		}
		seen[strings.ToLower(key)] = struct{}{}
		out = append(out, p)
	}
	if cfg == nil {
		return out
	}
	if r, err := Resolve(cfg, repoName); err == nil {
		add(r.Path)
	}
	if repoName != "" {
		add(OwnedClonePath(cfg, repoName))
		if cfg.DiscoveryRoot != "" {
			add(filepath.Join(cfg.DiscoveryRoot, repoName))
		}
	}
	for _, p := range cfg.Projects {
		add(p.WorkingRepoPath)
	}
	for _, p := range cfg.GitRepos {
		add(p)
	}
	return out
}

// EnsureOpenedWorkspace registers the opened folder (or IBIS_WORKSPACE) as a live project.
// Plugin mode replaces projects[] with that single workspace.
func EnsureOpenedWorkspace(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	start := workspaceStart(cfg)
	if start == "" {
		if !cfg.IsPlugin() {
			return nil
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("plugin workspace: %w", err)
		}
		start = cwd
	}
	root := start
	if gitRoot, ok := FindGitRoot(start); ok {
		root = gitRoot
	}
	name := filepath.Base(root)
	entry := config.ProjectConfig{Name: name, WorkingRepoPath: root}
	if cfg.IsPlugin() {
		cfg.Projects = []config.ProjectConfig{entry}
		return nil
	}
	for i, p := range cfg.Projects {
		if strings.EqualFold(p.Name, name) {
			if strings.TrimSpace(p.WorkingRepoPath) == "" {
				cfg.Projects[i].WorkingRepoPath = root
			}
			return nil
		}
	}
	cfg.Projects = append([]config.ProjectConfig{entry}, cfg.Projects...)
	return nil
}

// FindGitRoot walks up from start until a .git entry is found.
func FindGitRoot(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func workspaceStart(cfg *config.Config) string {
	if cfg != nil {
		if v := strings.TrimSpace(cfg.Workspace); v != "" {
			return v
		}
	}
	return strings.TrimSpace(os.Getenv("IBIS_WORKSPACE"))
}

func findProject(cfg *config.Config, name string) *config.ProjectConfig {
	if cfg == nil {
		return nil
	}
	if name != "" {
		for i := range cfg.Projects {
			if strings.EqualFold(strings.TrimSpace(cfg.Projects[i].Name), name) {
				return &cfg.Projects[i]
			}
		}
	}
	if cfg.IsPlugin() && len(cfg.Projects) == 1 {
		return &cfg.Projects[0]
	}
	return nil
}

func matchGitRepos(cfg *config.Config, name string) (string, bool) {
	if cfg == nil || name == "" {
		return "", false
	}
	for _, p := range cfg.GitRepos {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.EqualFold(filepath.Base(filepath.Clean(p)), name) {
			return p, true
		}
	}
	return "", false
}

func requireDir(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("not a directory")
	}
	return nil
}

func displayName(name, path string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return filepath.Base(path)
}
