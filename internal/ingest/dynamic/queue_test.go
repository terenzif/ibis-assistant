package dynamic

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/terenzif/ibis-server/internal/auth"
	"github.com/terenzif/ibis-server/internal/config"
)

type mockTokenProvider struct {
	token string
}

func (m *mockTokenProvider) GetGitToken(originUrl string) string {
	return m.token
}

func TestProjectIngestionManager_Enqueue_WorkspaceSync(t *testing.T) {
	// 1. Setup mock remote repository
	remoteDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init bare repo: %v", err)
	}

	// Create an initial commit in a dummy repo and push it to the bare repo
	dummyDir := t.TempDir()
	cmd = exec.Command("git", "init")
	cmd.Dir = dummyDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init dummy repo: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = dummyDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git email: %v", err)
	}

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dummyDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user: %v", err)
	}

	testFile := filepath.Join(dummyDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	cmd = exec.Command("git", "add", ".")
	cmd.Dir = dummyDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = dummyDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git commit: %v", err)
	}

	cmd = exec.Command("git", "remote", "add", "origin", remoteDir)
	cmd.Dir = dummyDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to add remote: %v", err)
	}

	cmd = exec.Command("git", "push", "origin", "master")
	cmd.Dir = dummyDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to push to bare repo: %v", err)
	}

	// 2. Initialize ProjectIngestionManager with mock TokenProvider
	discoveryRoot := t.TempDir()
	_ = context.WithValue(context.Background(), auth.TokenProviderKey, &mockTokenProvider{token: "mock-token"})
	cfg := &config.Config{
		DiscoveryRoot: discoveryRoot,
	}
	manager := NewProjectIngestionManager(cfg, nil)

	var processJobCalled bool
	manager.ProcessJob = func(ctx context.Context, job IngestionJob, repoPath string) error {
		processJobCalled = true
		return nil
	}

	syncDoneCh := make(chan IngestionResult, 1)
	completeCh := make(chan error, 1)

	// 3. Enqueue Job
	manager.Enqueue(IngestionJob{
		ProjectName: "test-repo",
		OriginURL:   remoteDir,
		Branch:      "master",
		OnSyncDone: func(res IngestionResult) {
			syncDoneCh <- res
		},
		OnComplete: func(err error) {
			completeCh <- err
		},
	})

	// 4. Wait for job completion
	select {
	case res := <-syncDoneCh:
		if res.Error != nil {
			t.Fatalf("Sync failed: %v", res.Error)
		}
		if !res.IsAligned {
			t.Errorf("Expected IsAligned to be true")
		}
		if res.ActualCommit == "" {
			t.Errorf("Expected ActualCommit to not be empty")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Timeout waiting for OnSyncDone")
	}

	select {
	case err := <-completeCh:
		if err != nil {
			t.Fatalf("Job completed with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Timeout waiting for OnComplete")
	}

	if !processJobCalled {
		t.Errorf("ProcessJob was not called")
	}

	manager.Stop()
}
