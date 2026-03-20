package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/deckonline/knowledge_mcp/internal/db"
)

type ConcurrencyMockRedmine struct {
	mu           sync.Mutex
	Active       int
	MaxActive    int
	CallCount    int
	SleepDuration time.Duration
}

func (m *ConcurrencyMockRedmine) IngestIssue(ctx context.Context, dbClient db.Executor, issueIDStr string) error {
	m.mu.Lock()
	m.Active++
	if m.Active > m.MaxActive {
		m.MaxActive = m.Active
	}
	m.CallCount++
	m.mu.Unlock()

	time.Sleep(m.SleepDuration)

	m.mu.Lock()
	m.Active--
	m.mu.Unlock()

	return nil
}

func TestIngestRepo_ConcurrencyBounded(t *testing.T) {
	// Setup Repo with multiple commits referencing different issues
	repoDir := t.TempDir()
	exec.Command("git", "init", repoDir).Run()
	exec.Command("git", "-C", repoDir, "config", "user.name", "Test").Run()
	exec.Command("git", "-C", repoDir, "config", "user.email", "test@example.com").Run()

	issueCount := 20
	for i := 0; i < issueCount; i++ {
		fname := filepath.Join(repoDir, fmt.Sprintf("file%d.txt", i))
		os.WriteFile(fname, []byte("content"), 0644)
		exec.Command("git", "-C", repoDir, "add", ".").Run()
		msg := fmt.Sprintf("Fix bug #%d", i)
		exec.Command("git", "-C", repoDir, "commit", "-m", msg).Run()
	}

	mockDB := &MockDB{}
	mockRedmine := &ConcurrencyMockRedmine{
		SleepDuration: 10 * time.Millisecond,
	}

	// Set concurrency to 5
	concurrency := 5

	// Run Ingest
	err := IngestRepo(context.Background(), mockDB, mockRedmine, repoDir, "test-repo", concurrency)
	if err != nil {
		t.Fatalf("IngestRepo failed: %v", err)
	}

	// Verify
	if mockRedmine.CallCount != issueCount {
		t.Errorf("Expected %d Redmine calls, got %d", issueCount, mockRedmine.CallCount)
	}

	// We expect MaxActive to be close to concurrency, but definitely not equal to issueCount (20).
	// It should be <= concurrency + 1 (maybe +1 due to race/timing, but strict semaphore should keep it <= concurrency).
	// Wait, the semaphore acquires BEFORE the goroutine starts. So Active should effectively be bounded by semaphore.
	// But `Active++` happens inside the goroutine.
	// So `Active` represents running goroutines.

	t.Logf("Max Concurrent Requests: %d (Limit: %d)", mockRedmine.MaxActive, concurrency)

	if mockRedmine.MaxActive > concurrency {
		t.Errorf("Concurrency limit exceeded! MaxActive: %d, Limit: %d", mockRedmine.MaxActive, concurrency)
	}
}
