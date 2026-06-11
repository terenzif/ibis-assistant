package dynamic

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/terenzif/ibis-arc/internal/config"
	"github.com/terenzif/ibis-arc/internal/db"
	"github.com/terenzif/ibis-arc/internal/logger"
)

// IngestionResult contains the result of the synchronous workspace sync phase.
type IngestionResult struct {
	ActualCommit string
	IsAligned    bool
	Error        error
}

// IngestionJob represents a request to synchronize and ingest a repository.
type IngestionJob struct {
	ProjectName string
	OriginURL   string
	Branch      string
	Commit      string
	OnSyncDone  func(IngestionResult) // Callback per la fase di sync (checkout/fetch)
	OnComplete  func(error)           // Callback per notificare il client al termine
}

// ProjectIngestionManager manages a queue of ingestion jobs per project.
type ProjectIngestionManager struct {
	mu     sync.Mutex
	queues map[string]chan IngestionJob
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	Config     *config.Config
	DB         db.Executor
	ProcessJob func(ctx context.Context, job IngestionJob, repoPath string) error
}

// NewProjectIngestionManager creates a new queue manager.
func NewProjectIngestionManager(cfg *config.Config, dbClient db.Executor) *ProjectIngestionManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProjectIngestionManager{
		queues: make(map[string]chan IngestionJob),
		ctx:    ctx,
		cancel: cancel,
		Config: cfg,
		DB:     dbClient,
	}
}

// Enqueue adds a job to the project's queue, creating a worker if one doesn't exist.
func (m *ProjectIngestionManager) Enqueue(job IngestionJob) {
	m.mu.Lock()
	defer m.mu.Unlock()

	q, exists := m.queues[job.ProjectName]
	if !exists {
		// Buffered channel to prevent blocking the caller immediately
		q = make(chan IngestionJob, 100)
		m.queues[job.ProjectName] = q

		m.wg.Add(1)
		go m.worker(job.ProjectName, q)
	}

	select {
	case q <- job:
		logger.Debug("Job accodato per il progetto %s (Branch: %s)", job.ProjectName, job.Branch)
	default:
		logger.Warn("Coda piena per il progetto %s. Job scartato.", job.ProjectName)
		if job.OnComplete != nil {
			job.OnComplete(fmt.Errorf("queue is full"))
		}
	}
}

// Stop gracefully shuts down all workers.
func (m *ProjectIngestionManager) Stop() {
	logger.Info("Arresto del ProjectIngestionManager...")
	m.cancel() // Segnala ai worker di fermarsi
	m.wg.Wait()
	logger.Info("ProjectIngestionManager arrestato.")
}

func (m *ProjectIngestionManager) worker(projectName string, q chan IngestionJob) {
	defer m.wg.Done()
	logger.Info("Worker avviato per il progetto: %s", projectName)

	for {
		select {
		case <-m.ctx.Done():
			logger.Info("Worker terminato per cancellazione contesto: %s", projectName)
			return
		case job, ok := <-q:
			if !ok {
				return
			}
			m.executeJob(job)
		}
	}
}

func (m *ProjectIngestionManager) executeJob(job IngestionJob) {
	logger.Info("Inizio esecuzione job per %s (Branch: %s, Commit: %s)", job.ProjectName, job.Branch, job.Commit)

	// 1. Sync Workspace
	repoPath, actualCommit, isAligned, err := SyncWorkspace(m.ctx, m.Config, m.DB, job.ProjectName, job.OriginURL, job.Branch, job.Commit)

	if job.OnSyncDone != nil {
		job.OnSyncDone(IngestionResult{
			ActualCommit: actualCommit,
			IsAligned:    isAligned,
			Error:        err,
		})
	}

	if err != nil {
		logger.Error("Errore sincronizzazione workspace per %s: %v", job.ProjectName, err)
		if job.OnComplete != nil {
			job.OnComplete(err)
		}
		return
	}

	// 2. Process Job (IngestRepo, IngestCodebase)
	if m.ProcessJob != nil {
		// Diamo un timeout generoso per l'ingestione
		jobCtx, cancel := context.WithTimeout(m.ctx, 2*time.Hour)
		defer cancel()

		err = m.ProcessJob(jobCtx, job, repoPath)
		if err != nil {
			logger.Error("Errore ingestione per %s: %v", job.ProjectName, err)
		} else {
			logger.Info("Ingestione completata con successo per %s", job.ProjectName)
		}
	}

	// 3. Notify
	if job.OnComplete != nil {
		job.OnComplete(err)
	}
}
