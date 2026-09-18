package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/terenzif/ibis-assistant/internal/config"
)

func TestResolveServerOwnedClone(t *testing.T) {
	cfg := &config.Config{RuntimeMode: "server", DiscoveryRoot: `C:\data\repos`}
	r, err := Resolve(cfg, "ibis-assistant")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Owned {
		t.Fatal("server workspace must be owned")
	}
	want := filepath.Join(`C:\data\repos`, "dynamic", "ibis-assistant")
	if r.Path != want {
		t.Fatalf("path=%s want %s", r.Path, want)
	}
}

func TestResolveEmptyModeOwnedClone(t *testing.T) {
	cfg := &config.Config{DiscoveryRoot: t.TempDir()}
	r, err := Resolve(cfg, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Owned {
		t.Fatal("empty runtime_mode must keep owned clones")
	}
	if r.Path != filepath.Join(cfg.DiscoveryRoot, "dynamic", "demo") {
		t.Fatalf("path=%s", r.Path)
	}
}

func TestResolvePersonalWorkingRepoPath(t *testing.T) {
	dir := initGitDir(t)
	cfg := &config.Config{
		RuntimeMode:   "personal",
		DiscoveryRoot: t.TempDir(),
		Projects: []config.ProjectConfig{
			{Name: "ibis-assistant", WorkingRepoPath: dir},
		},
	}
	r, err := Resolve(cfg, "ibis-assistant")
	if err != nil {
		t.Fatal(err)
	}
	if r.Owned {
		t.Fatal("personal workspace must not be owned")
	}
	if r.Path != dir {
		t.Fatalf("path=%s want %s", r.Path, dir)
	}
}

func TestResolvePersonalGitReposBasename(t *testing.T) {
	dir := initGitDir(t)
	cfg := &config.Config{
		RuntimeMode: "personal",
		GitRepos:    []string{dir},
	}
	r, err := Resolve(cfg, filepath.Base(dir))
	if err != nil {
		t.Fatal(err)
	}
	if r.Owned || r.Path != dir {
		t.Fatalf("resolved %+v", r)
	}
}

func TestResolvePersonalMissingPathError(t *testing.T) {
	cfg := &config.Config{
		RuntimeMode: "personal",
		Projects: []config.ProjectConfig{
			{Name: "gone", WorkingRepoPath: filepath.Join(t.TempDir(), "does-not-exist")},
		},
	}
	_, err := Resolve(cfg, "gone")
	if err == nil {
		t.Fatal("expected missing-path error")
	}
}

func TestResolvePersonalNoMatchError(t *testing.T) {
	t.Setenv("IBIS_WORKSPACE", "")
	empty := t.TempDir()
	t.Chdir(empty)
	cfg := &config.Config{RuntimeMode: "personal"}
	_, err := Resolve(cfg, "unknown-project")
	if err == nil {
		t.Fatal("expected error when no live path exists")
	}
}

func TestResolvePluginWalksWorkspace(t *testing.T) {
	root := initGitDir(t)
	nested := filepath.Join(root, "internal", "pkg")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{RuntimeMode: "plugin", Workspace: nested}
	r, err := Resolve(cfg, "whatever")
	if err != nil {
		t.Fatal(err)
	}
	if r.Owned {
		t.Fatal("plugin must use live tree")
	}
	if r.Path != root {
		t.Fatalf("path=%s want git root %s", r.Path, root)
	}
}

func TestCandidatePathsIncludesResolved(t *testing.T) {
	dir := initGitDir(t)
	cfg := &config.Config{
		RuntimeMode:   "personal",
		DiscoveryRoot: t.TempDir(),
		Projects:      []config.ProjectConfig{{Name: "demo", WorkingRepoPath: dir}},
	}
	cands := CandidatePaths(cfg, "demo")
	if len(cands) == 0 || cands[0] != dir {
		t.Fatalf("candidates=%v want live path first", cands)
	}
}

func TestEnsureOpenedWorkspacePluginReplaces(t *testing.T) {
	root := initGitDir(t)
	cfg := &config.Config{
		RuntimeMode: "plugin",
		Workspace:   root,
		Projects:    []config.ProjectConfig{{Name: "other", WorkingRepoPath: `C:\old`}},
	}
	if err := EnsureOpenedWorkspace(cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 1 || cfg.Projects[0].WorkingRepoPath != root {
		t.Fatalf("projects=%v", cfg.Projects)
	}
	if cfg.Projects[0].Name != filepath.Base(root) {
		t.Fatalf("name=%s", cfg.Projects[0].Name)
	}
}

func TestEnsureOpenedWorkspacePluginSkipsNonGitCwd(t *testing.T) {
	t.Setenv("IBIS_WORKSPACE", "")
	empty := t.TempDir()
	t.Chdir(empty)
	cfg := &config.Config{
		RuntimeMode: "plugin",
		Projects:    []config.ProjectConfig{{Name: "stale", WorkingRepoPath: `C:\old`}},
	}
	if err := EnsureOpenedWorkspace(cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 0 {
		t.Fatalf("plugin must not bind a non-git cwd as a project: %v", cfg.Projects)
	}
	if PluginHasBoundWorkspace(cfg) {
		t.Fatal("empty plugin cwd must not be ready")
	}
}

func TestWorkspaceStartIgnoresUnexpandedFolder(t *testing.T) {
	t.Setenv("IBIS_WORKSPACE", "${workspaceFolder}")
	t.Setenv("CURSOR_WORKSPACE_FOLDER", "")
	t.Setenv("CURSOR_WORKSPACE", "")
	t.Setenv("VSCODE_WORKSPACE_FOLDER", "")
	if got := workspaceStart(&config.Config{RuntimeMode: "plugin"}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestWorkspaceStartUsesCursorFolderEnv(t *testing.T) {
	t.Setenv("IBIS_WORKSPACE", "")
	t.Setenv("CURSOR_WORKSPACE_FOLDER", `C:\devsrc\ibis-assistant`)
	if got := workspaceStart(&config.Config{RuntimeMode: "plugin"}); got != `C:\devsrc\ibis-assistant` {
		t.Fatalf("got %q", got)
	}
}

func TestFileURIToPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		want := filepath.Clean(`C:\devsrc\ibis-assistant`)
		got := FileURIToPath("file:///C:/devsrc/ibis-assistant")
		if got != want {
			t.Fatalf("file URI got %q want %q", got, want)
		}
		got = FileURIToPath(`c:\devsrc\ibis-assistant`)
		if !strings.EqualFold(got, want) {
			t.Fatalf("drive path got %q want %q", got, want)
		}
		return
	}
	got := FileURIToPath("file:///tmp/ibis-assistant")
	if got != "/tmp/ibis-assistant" {
		t.Fatalf("posix file URI got %q", got)
	}
}

func initGitDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}
