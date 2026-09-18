package dynamic

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/terenzif/ibis-assistant/internal/config"
)

func TestSyncWorkspacePersonalSkipsDestructiveGit(t *testing.T) {
	live := initCommitRepo(t)
	dirty := filepath.Join(live, "dirty.txt")
	if err := os.WriteFile(dirty, []byte("uncommitted"), 0644); err != nil {
		t.Fatal(err)
	}
	discovery := t.TempDir()
	cfg := &config.Config{
		RuntimeMode:   "personal",
		DiscoveryRoot: discovery,
		Projects: []config.ProjectConfig{
			{Name: "demo", WorkingRepoPath: live},
		},
	}
	path, commit, aligned, err := SyncWorkspace(context.Background(), cfg, nil, "demo", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !aligned {
		t.Fatal("live tree should be treated as aligned")
	}
	if path != live {
		t.Fatalf("path=%s want %s", path, live)
	}
	if commit == "" {
		t.Fatal("expected HEAD commit")
	}
	got, err := os.ReadFile(dirty)
	if err != nil {
		t.Fatalf("dirty file was removed: %v", err)
	}
	if string(got) != "uncommitted" {
		t.Fatalf("dirty file changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(discovery, "dynamic", "demo")); err == nil {
		t.Fatal("personal mode must not clone under dynamic/")
	}
}

func TestSyncWorkspaceMissingPersonalPath(t *testing.T) {
	cfg := &config.Config{
		RuntimeMode: "personal",
		Projects: []config.ProjectConfig{
			{Name: "demo", WorkingRepoPath: filepath.Join(t.TempDir(), "missing")},
		},
	}
	_, _, _, err := SyncWorkspace(context.Background(), cfg, nil, "demo", "", "", "")
	if err == nil {
		t.Fatal("expected error for missing live path")
	}
}

func TestSyncWorkspaceServerClonesUnderDynamic(t *testing.T) {
	src := initCommitRepo(t)
	discovery := t.TempDir()
	cfg := &config.Config{
		RuntimeMode:   "server",
		DiscoveryRoot: discovery,
	}
	path, commit, aligned, err := SyncWorkspace(context.Background(), cfg, nil, "demo", src, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !aligned {
		t.Fatal("clone should be aligned")
	}
	want := filepath.Join(discovery, "dynamic", "demo")
	if path != want {
		t.Fatalf("path=%s want %s", path, want)
	}
	if commit == "" {
		t.Fatal("expected cloned HEAD")
	}
	if _, err := os.Stat(filepath.Join(want, ".git")); err != nil {
		t.Fatalf("expected clone at %s: %v", want, err)
	}
}

func initCommitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
