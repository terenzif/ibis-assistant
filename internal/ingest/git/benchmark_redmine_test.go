package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkIngestRedmineMixed simulates a scenario where commits are mixed:
// Some have issues (slow due to Redmine), some don't.
// With blocking concurrency (semaphore), the "fast" commits are delayed by the "slow" ones.
// With a buffered worker pool, the fast commits can proceed while workers process the slow ones.
func BenchmarkIngestRedmineMixed(b *testing.B) {
	// 1. Setup Repo
	repoDir := b.TempDir()
	exec.Command("git", "init", repoDir).Run()
	exec.Command("git", "-C", repoDir, "config", "user.name", "Bench").Run()
	exec.Command("git", "-C", repoDir, "config", "user.email", "bench@example.com").Run()

	// Create 200 commits:
	// 0-49: Issue (Slow)
	// 50-99: No Issue (Fast)
	// 100-149: Issue (Slow)
	// 150-199: No Issue (Fast)
	totalCommits := 200
	for i := 0; i < totalCommits; i++ {
		fname := filepath.Join(repoDir, fmt.Sprintf("file%d.txt", i))
		os.WriteFile(fname, []byte("content"), 0644)
		exec.Command("git", "-C", repoDir, "add", ".").Run()

		msg := fmt.Sprintf("Commit %d", i)
		if (i >= 0 && i < 50) || (i >= 100 && i < 150) {
			msg = fmt.Sprintf("Fix bug #%d", i)
		}
		exec.Command("git", "-C", repoDir, "commit", "-m", msg).Run()
	}

	// 2. Setup Mock Clients
	mockDB := &MockDB{}
	mockRedmine := &ConcurrencyMockRedmine{
		SleepDuration: 10 * time.Millisecond, // 10ms per issue
	}

	// 3. Run Ingest
	// Concurrency = 5
	concurrency := 5

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset mock state? The mock state is minimal.
		// Re-ingest the same repo.
		// Since we modify the repo in loop, this might be tricky if IngestRepo alters state?
		// IngestRepo reads git log. It doesn't modify git repo.
		// It modifies DB. MockDB is just a sink.
		// However, MockRedmine counts calls. We should reset call count if we care, but for benchmarking speed it doesn't matter much.
		// But strictly, we should re-init mocks or reset them.

		// Reset MockRedmine counters if needed?
		mockRedmine.Active = 0
		mockRedmine.CallCount = 0

		err := IngestRepo(context.Background(), mockDB, mockRedmine, repoDir, "test-repo", concurrency)
		if err != nil {
			b.Fatalf("IngestRepo failed: %v", err)
		}
	}
}
